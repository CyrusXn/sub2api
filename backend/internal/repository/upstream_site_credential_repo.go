package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const upstreamSiteAccountsQuery = `
SELECT id, name, credentials->>'base_url'
FROM accounts
WHERE platform = 'openai'
  AND type = 'apikey'
  AND deleted_at IS NULL
  AND COALESCE(credentials->>'base_url', '') <> ''
ORDER BY id`

const upstreamSiteCredentialsQuery = `
SELECT host, login_username, password_encrypted
FROM upstream_site_credentials
ORDER BY host`

const upstreamSiteCredentialGetQuery = `
SELECT host, login_username, password_encrypted, created_at, updated_at
FROM upstream_site_credentials
WHERE host = $1`

const upstreamSiteCredentialUpsertQuery = `
INSERT INTO upstream_site_credentials (host, login_username, password_encrypted)
VALUES ($1, $2, $3)
ON CONFLICT (host) DO UPDATE SET
    login_username = EXCLUDED.login_username,
    password_encrypted = EXCLUDED.password_encrypted,
    updated_at = NOW()`

const upstreamSiteCredentialDeleteQuery = `DELETE FROM upstream_site_credentials WHERE host = $1`

type upstreamSiteCredentialRepository struct {
	db *sql.DB
}

func NewUpstreamSiteCredentialRepository(db *sql.DB) service.UpstreamSiteCredentialRepository {
	return &upstreamSiteCredentialRepository{db: db}
}

func (r *upstreamSiteCredentialRepository) ListSites(ctx context.Context) ([]service.UpstreamSiteCredentialSummary, error) {
	rows, err := r.db.QueryContext(ctx, upstreamSiteAccountsQuery)
	if err != nil {
		return nil, fmt.Errorf("list upstream site accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	sitesByHost := make(map[string]*service.UpstreamSiteCredentialSummary)
	for rows.Next() {
		var id int64
		var name, baseURL string
		if err := rows.Scan(&id, &name, &baseURL); err != nil {
			return nil, fmt.Errorf("scan upstream site account: %w", err)
		}
		host, websiteURL, ok := repositoryNormalizeUpstreamSite(baseURL)
		if !ok {
			continue
		}
		site := sitesByHost[host]
		if site == nil {
			protocol := "innom"
			if host == "api.aigclink.xyz" {
				protocol = "newapi"
			}
			site = &service.UpstreamSiteCredentialSummary{
				Host:       host,
				WebsiteURL: websiteURL,
				Protocol:   protocol,
			}
			sitesByHost[host] = site
		}
		site.AccountIDs = append(site.AccountIDs, id)
		site.AccountNames = append(site.AccountNames, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate upstream site accounts: %w", err)
	}

	credentialRows, err := r.db.QueryContext(ctx, upstreamSiteCredentialsQuery)
	if err != nil {
		return nil, fmt.Errorf("list upstream site credentials: %w", err)
	}
	defer func() { _ = credentialRows.Close() }()
	for credentialRows.Next() {
		var host, username, ciphertext string
		if err := credentialRows.Scan(&host, &username, &ciphertext); err != nil {
			return nil, fmt.Errorf("scan upstream site credential: %w", err)
		}
		if site := sitesByHost[strings.ToLower(strings.TrimSpace(host))]; site != nil {
			site.LoginUsername = username
			site.HasPassword = strings.TrimSpace(ciphertext) != ""
		}
	}
	if err := credentialRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate upstream site credentials: %w", err)
	}

	sites := make([]service.UpstreamSiteCredentialSummary, 0, len(sitesByHost))
	for _, site := range sitesByHost {
		sites = append(sites, *site)
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].Host < sites[j].Host })
	return sites, nil
}

func (r *upstreamSiteCredentialRepository) GetByHost(ctx context.Context, host string) (*service.UpstreamSiteCredential, error) {
	credential := &service.UpstreamSiteCredential{}
	err := r.db.QueryRowContext(ctx, upstreamSiteCredentialGetQuery, host).Scan(
		&credential.Host,
		&credential.LoginUsername,
		&credential.PasswordCiphertext,
		&credential.CreatedAt,
		&credential.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get upstream site credential: %w", err)
	}
	return credential, nil
}

func (r *upstreamSiteCredentialRepository) Upsert(ctx context.Context, credential *service.UpstreamSiteCredential) error {
	if credential == nil {
		return service.ErrUpstreamSiteCredentialInvalid
	}
	_, err := r.db.ExecContext(
		ctx,
		upstreamSiteCredentialUpsertQuery,
		credential.Host,
		credential.LoginUsername,
		credential.PasswordCiphertext,
	)
	if err != nil {
		return fmt.Errorf("upsert upstream site credential: %w", err)
	}
	return nil
}

func (r *upstreamSiteCredentialRepository) Delete(ctx context.Context, host string) error {
	if _, err := r.db.ExecContext(ctx, upstreamSiteCredentialDeleteQuery, host); err != nil {
		return fmt.Errorf("delete upstream site credential: %w", err)
	}
	return nil
}

func repositoryNormalizeUpstreamSite(rawURL string) (string, string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", "", false
	}
	host := strings.ToLower(parsed.Hostname())
	websiteURL := strings.ToLower(parsed.Scheme) + "://" + host
	if port := parsed.Port(); port != "" {
		websiteURL += ":" + port
	}
	return host, websiteURL, true
}
