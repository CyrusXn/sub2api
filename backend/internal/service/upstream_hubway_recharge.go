package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

type UpstreamSiteRecharge struct {
	ID     int64
	Amount float64
	UsedAt time.Time
}

func IsHubwayRechargeSite(host string) bool {
	switch strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".") {
	case "hubway.cc", "z.hubway.cc":
		return true
	}
	return false
}

func (s *UpstreamBillingProbeService) fetchHubwayRecharges(ctx context.Context, item UpstreamSiteBalanceObservation, account *Account, proxyURL string, profile *tlsfingerprint.Profile, now time.Time) ([]UpstreamSiteRecharge, time.Time, error) {
	baseURL, err := s.accountTestService.validateUpstreamBaseURL(item.BaseURL)
	if err != nil || mustUpstreamHost(baseURL) != item.Host {
		return nil, time.Time{}, errors.New("站点地址无效")
	}
	if s.upstreamSites == nil {
		return nil, time.Time{}, errors.New("未配置网页登录")
	}
	credential, err := s.upstreamSites.Resolve(ctx, baseURL)
	if err != nil || credential == nil {
		return nil, time.Time{}, errors.New("未配置网页登录")
	}
	token, _, err := s.upstreamSiteBalanceToken(ctx, account, baseURL, credential.Username, credential.Password, proxyURL, profile, now)
	if err != nil {
		return nil, time.Time{}, err
	}
	return collectHubwayRecharges(item.RechargeHistorySince, item.RechargeConversionScale, func(page int) (hubwayHistoryPage, error) {
		fetch := func() (hubwayHistoryPage, int, error) {
			return s.fetchHubwayHistoryPage(ctx, account, baseURL, token, proxyURL, profile, now, page)
		}
		data, status, err := fetch()
		if status == http.StatusUnauthorized {
			s.clearUpstreamSiteBalanceToken(baseURL, credential.Username)
			token, _, err = s.upstreamSiteBalanceToken(ctx, account, baseURL, credential.Username, credential.Password, proxyURL, profile, now)
			if err != nil {
				return hubwayHistoryPage{}, err
			}
			data, _, err = fetch()
		}
		return data, err
	})
}

const hubwayHistoryPageSize = 100

type hubwayHistoryItem struct {
	ID     int64     `json:"id"`
	Type   string    `json:"type"`
	Status string    `json:"status"`
	Value  float64   `json:"value"`
	UsedAt time.Time `json:"used_at"`
}

type hubwayHistoryPage struct {
	Total *int                `json:"total"`
	Items []hubwayHistoryItem `json:"items"`
}

func (s *UpstreamBillingProbeService) fetchHubwayHistoryPage(ctx context.Context, account *Account, baseURL, token, proxyURL string, profile *tlsfingerprint.Profile, now time.Time, page int) (hubwayHistoryPage, int, error) {
	query := url.Values{"page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(hubwayHistoryPageSize)}}
	req, cancel, err := s.newWebAccountRequest(ctx, http.MethodGet, baseURL, "/api/v1/redeem/history", query, nil)
	if err != nil {
		return hubwayHistoryPage{}, 0, errors.New("无法构造流水请求")
	}
	defer cancel()
	req.Header.Set("Authorization", "Bearer "+token)
	_, status, reason, _, body := s.doWebAccountRequest(account, req, proxyURL, profile, now)
	if reason != "" {
		return hubwayHistoryPage{}, status, errors.New(reason)
	}
	var response struct {
		Code *int              `json:"code"`
		Data hubwayHistoryPage `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil || response.Code == nil || *response.Code != 0 || response.Data.Total == nil || response.Data.Items == nil {
		return hubwayHistoryPage{}, status, errors.New("流水响应无效")
	}
	return response.Data, status, nil
}

func collectHubwayRecharges(since time.Time, scale float64, fetch func(int) (hubwayHistoryPage, error)) ([]UpstreamSiteRecharge, time.Time, error) {
	latest := since
	total := 0
	var previous hubwayHistoryItem
	var recharges []UpstreamSiteRecharge
	// 分页失败或发生排序漂移时整批重试，避免漏记被跳过的流水。
	for page := 1; page <= 50; page++ {
		data, err := fetch(page)
		if err != nil {
			return nil, time.Time{}, err
		}
		if page == 1 && data.Total != nil {
			total = *data.Total
		}
		offset := (page - 1) * hubwayHistoryPageSize
		if data.Total == nil || *data.Total != total || total < offset || len(data.Items) != min(total-offset, hubwayHistoryPageSize) {
			return nil, time.Time{}, errors.New("流水分页不完整")
		}
		for _, entry := range data.Items {
			if entry.UsedAt.IsZero() || entry.ID <= 0 || (!previous.UsedAt.IsZero() && (entry.UsedAt.After(previous.UsedAt) || (entry.UsedAt.Equal(previous.UsedAt) && entry.ID >= previous.ID))) {
				return nil, time.Time{}, errors.New("流水顺序或时间无效")
			}
			previous = entry
			if entry.UsedAt.Before(since) {
				return recharges, latest, nil
			}
			if entry.UsedAt.After(latest) {
				latest = entry.UsedAt
			}
			if entry.Type != RedeemTypeBalance || entry.Status != StatusUsed {
				continue
			}
			amount := entry.Value * scale
			if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) || amount >= 1e20 {
				return nil, time.Time{}, errors.New("余额充值金额无效")
			}
			recharges = append(recharges, UpstreamSiteRecharge{ID: entry.ID, Amount: amount, UsedAt: entry.UsedAt})
		}
		if offset+len(data.Items) == total {
			return recharges, latest, nil
		}
	}
	return nil, time.Time{}, errors.New("流水超出单次分页上限")
}
