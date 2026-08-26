package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var (
	ErrUpstreamSiteCredentialInvalid     = errors.New("invalid upstream site credential")
	ErrUpstreamSiteCredentialUnavailable = errors.New("upstream site credential service unavailable")
)

type UpstreamSiteCredential struct {
	Host               string
	DisplayName        string
	LoginUsername      string
	PasswordCiphertext string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type UpstreamSiteCredentialSummary struct {
	Host          string   `json:"host"`
	WebsiteURL    string   `json:"website_url"`
	AccountIDs    []int64  `json:"account_ids"`
	AccountNames  []string `json:"account_names"`
	LoginUsername string   `json:"login_username"`
	HasPassword   bool     `json:"has_password"`
	Protocol      string   `json:"protocol"`
	DisplayName   string   `json:"display_name"`
}

func defaultUpstreamSiteDisplayName(accountNames []string, host string) string {
	for _, name := range accountNames {
		start := strings.Index(name, "【")
		if start < 0 {
			continue
		}
		end := strings.Index(name[start+len("【"):], "】")
		if end >= 0 {
			label := strings.TrimSpace(name[start+len("【") : start+len("【")+end])
			if label != "" {
				return label
			}
		}
	}
	return strings.TrimSpace(host)
}

type UpstreamSiteCredentialInput struct {
	BaseURL     string
	DisplayName string
	Username    string
	Password    string
}

type ResolvedUpstreamSiteCredential struct {
	Host     string
	Username string
	Password string
}

type UpstreamSiteCredentialRepository interface {
	ListSites(ctx context.Context) ([]UpstreamSiteCredentialSummary, error)
	GetByHost(ctx context.Context, host string) (*UpstreamSiteCredential, error)
	Upsert(ctx context.Context, credential *UpstreamSiteCredential) error
	Delete(ctx context.Context, host string) error
}

type UpstreamSiteCredentialService struct {
	repo      UpstreamSiteCredentialRepository
	encryptor SecretEncryptor
}

func NewUpstreamSiteCredentialService(
	repo UpstreamSiteCredentialRepository,
	encryptor SecretEncryptor,
) *UpstreamSiteCredentialService {
	return &UpstreamSiteCredentialService{repo: repo, encryptor: encryptor}
}

func (s *UpstreamSiteCredentialService) List(ctx context.Context) ([]UpstreamSiteCredentialSummary, error) {
	if s == nil || s.repo == nil {
		return nil, ErrUpstreamSiteCredentialUnavailable
	}
	sites, err := s.repo.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	for i := range sites {
		if strings.TrimSpace(sites[i].DisplayName) == "" {
			sites[i].DisplayName = defaultUpstreamSiteDisplayName(sites[i].AccountNames, sites[i].Host)
		}
	}
	return sites, nil
}

func (s *UpstreamSiteCredentialService) Upsert(
	ctx context.Context,
	input UpstreamSiteCredentialInput,
) (*UpstreamSiteCredentialSummary, error) {
	if s == nil || s.repo == nil || s.encryptor == nil {
		return nil, ErrUpstreamSiteCredentialUnavailable
	}
	host, websiteURL, err := normalizeUpstreamSite(input.BaseURL)
	if err != nil {
		return nil, err
	}
	username := strings.TrimSpace(input.Username)
	if username == "" {
		return nil, fmt.Errorf("%w: login username is required", ErrUpstreamSiteCredentialInvalid)
	}

	existing, err := s.repo.GetByHost(ctx, host)
	if err != nil {
		return nil, err
	}
	passwordCiphertext := ""
	if existing != nil {
		passwordCiphertext = existing.PasswordCiphertext
	}
	if password := input.Password; password != "" {
		passwordCiphertext, err = s.encryptor.Encrypt(password)
		if err != nil {
			return nil, fmt.Errorf("encrypt upstream site password: %w", err)
		}
	}
	if passwordCiphertext == "" {
		return nil, fmt.Errorf("%w: login password is required", ErrUpstreamSiteCredentialInvalid)
	}

	credential := &UpstreamSiteCredential{
		Host:               host,
		DisplayName:        strings.TrimSpace(input.DisplayName),
		LoginUsername:      username,
		PasswordCiphertext: passwordCiphertext,
	}
	if err := s.repo.Upsert(ctx, credential); err != nil {
		return nil, err
	}
	return &UpstreamSiteCredentialSummary{
		Host:          host,
		WebsiteURL:    websiteURL,
		LoginUsername: username,
		HasPassword:   true,
		Protocol:      upstreamSiteProtocolForHost(host),
		DisplayName:   credential.DisplayName,
	}, nil
}

func (s *UpstreamSiteCredentialService) Resolve(
	ctx context.Context,
	baseURL string,
) (*ResolvedUpstreamSiteCredential, error) {
	if s == nil || s.repo == nil || s.encryptor == nil {
		return nil, ErrUpstreamSiteCredentialUnavailable
	}
	host, _, err := normalizeUpstreamSite(baseURL)
	if err != nil {
		return nil, err
	}
	credential, err := s.repo.GetByHost(ctx, host)
	if err != nil || credential == nil {
		return nil, err
	}
	password, err := s.encryptor.Decrypt(credential.PasswordCiphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt upstream site password: %w", err)
	}
	return &ResolvedUpstreamSiteCredential{
		Host:     host,
		Username: credential.LoginUsername,
		Password: password,
	}, nil
}

func (s *UpstreamSiteCredentialService) Delete(ctx context.Context, baseURL string) error {
	if s == nil || s.repo == nil {
		return ErrUpstreamSiteCredentialUnavailable
	}
	host, _, err := normalizeUpstreamSite(baseURL)
	if err != nil {
		return err
	}
	return s.repo.Delete(ctx, host)
}

func normalizeUpstreamSite(rawURL string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return "", "", fmt.Errorf("%w: invalid base URL", ErrUpstreamSiteCredentialInvalid)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("%w: unsupported URL scheme", ErrUpstreamSiteCredentialInvalid)
	}
	host := strings.ToLower(parsed.Hostname())
	websiteURL := strings.ToLower(parsed.Scheme) + "://" + host
	if port := parsed.Port(); port != "" {
		websiteURL += ":" + port
	}
	return host, websiteURL, nil
}

func upstreamSiteProtocolForHost(host string) string {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "api.aigclink.xyz", "ai.pite.chat":
		return "newapi"
	case "hubway.cc", "mxamaxai.com", "ai.maok.shop":
		return "innom"
	}
	return "innom"
}
