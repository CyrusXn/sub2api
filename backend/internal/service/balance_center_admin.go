package service

import (
	"context"
	"errors"
	"strings"
	"time"
)

type BalanceCenterListFilter struct {
	Page      int
	PageSize  int
	SiteID    int64
	AccountID int64
	Status    string
	StartTime *time.Time
	EndTime   *time.Time
}

type BalanceCenterOverviewItem struct {
	SiteID           int64      `json:"site_id"`
	SiteName         string     `json:"site_name"`
	NormalizedDomain string     `json:"normalized_domain"`
	BaseURL          string     `json:"base_url"`
	AccountID        *int64     `json:"account_id,omitempty"`
	AccountName      string     `json:"account_name"`
	Status           string     `json:"status"`
	Balance          *float64   `json:"balance,omitempty"`
	ConvertedBalance *float64   `json:"converted_balance,omitempty"`
	RateMultiplier   *float64   `json:"rate_multiplier,omitempty"`
	ConversionScale  float64    `json:"conversion_scale"`
	Currency         string     `json:"currency"`
	Reason           string     `json:"reason"`
	ProbedAt         time.Time  `json:"probed_at"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
	Source           string     `json:"source"`
}

type BalanceCenterSite struct {
	ID                      int64     `json:"id"`
	Name                    string    `json:"name"`
	NormalizedDomain        string    `json:"normalized_domain"`
	BaseURL                 string    `json:"base_url"`
	Source                  string    `json:"source"`
	ProbeSupported          bool      `json:"probe_supported"`
	HistoricalRechargeTotal float64   `json:"historical_recharge_total"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type BalanceCenterManualRow struct {
	ID         int64   `json:"id"`
	Source     string  `json:"source"`
	SourceKey  string  `json:"source_key"`
	SiteID     *int64  `json:"site_id,omitempty"`
	Label      string  `json:"label"`
	Expression string  `json:"expression"`
	Amount     float64 `json:"amount"`
	SortOrder  int     `json:"sort_order"`
}

type BalanceCenterRechargeEvent struct {
	ID         int64     `json:"id"`
	Source     string    `json:"source"`
	SourceKey  string    `json:"source_key"`
	SiteID     *int64    `json:"site_id,omitempty"`
	SiteLabel  string    `json:"site_label,omitempty"`
	AccountID  *int64    `json:"account_id,omitempty"`
	Amount     float64   `json:"amount"`
	Currency   string    `json:"currency"`
	OccurredAt time.Time `json:"occurred_at"`
	Note       string    `json:"note"`
}

type BalanceCenterRechargeSiteSummary struct {
	SiteID      *int64                       `json:"site_id"`
	SiteName    string                       `json:"site_name"`
	TotalAmount float64                      `json:"total_amount"`
	RecordCount int64                        `json:"record_count"`
	Items       []BalanceCenterRechargeEvent `json:"items"`
}

type BalanceCenterRechargeSummary struct {
	TotalAmount float64                            `json:"total_amount"`
	Items       []BalanceCenterRechargeEvent       `json:"items"`
	Sites       []BalanceCenterRechargeSiteSummary `json:"sites"`
	Total       int64                              `json:"total"`
	Page        int                                `json:"page"`
	PageSize    int                                `json:"page_size"`
}

type BalanceCenterReconciliation struct {
	ID               int64      `json:"id"`
	Source           string     `json:"source"`
	SourceKey        string     `json:"source_key"`
	PeriodStart      *time.Time `json:"period_start,omitempty"`
	PeriodEnd        *time.Time `json:"period_end,omitempty"`
	ExpectedAmount   *float64   `json:"expected_amount,omitempty"`
	ActualAmount     *float64   `json:"actual_amount,omitempty"`
	DifferenceAmount *float64   `json:"difference_amount,omitempty"`
	Status           string     `json:"status"`
	CreatedAt        time.Time  `json:"created_at"`
}

type BalanceCenterAlertDelivery struct {
	ID            int64      `json:"id"`
	SiteID        int64      `json:"site_id"`
	AccountID     *int64     `json:"account_id,omitempty"`
	SnapshotID    *int64     `json:"snapshot_id,omitempty"`
	AlertType     string     `json:"alert_type"`
	Recipient     string     `json:"recipient_email"`
	OldValue      *float64   `json:"old_value,omitempty"`
	NewValue      *float64   `json:"new_value,omitempty"`
	Threshold     *float64   `json:"threshold,omitempty"`
	Status        string     `json:"status"`
	FailureReason string     `json:"failure_reason"`
	AttemptedAt   *time.Time `json:"attempted_at,omitempty"`
	AcceptedAt    *time.Time `json:"accepted_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

type BalanceCenterPage[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

type BalanceCenterAdminRepository interface {
	ListBalanceCenterOverview(context.Context) ([]BalanceCenterOverviewItem, error)
	ListBalanceCenterSites(context.Context) ([]BalanceCenterSite, error)
	ListBalanceCenterSnapshots(context.Context, BalanceCenterListFilter) (*BalanceCenterPage[BalanceCenterSnapshot], error)
	ListBalanceCenterManualRows(context.Context) ([]BalanceCenterManualRow, error)
	ReplaceBalanceCenterManualRows(context.Context, []BalanceCenterManualRow) error
	ListBalanceCenterRechargeEvents(context.Context, BalanceCenterListFilter) (*BalanceCenterPage[BalanceCenterRechargeEvent], error)
	GetBalanceCenterRechargeSummary(context.Context, BalanceCenterListFilter) (*BalanceCenterRechargeSummary, error)
	CreateBalanceCenterRechargeEvent(context.Context, *BalanceCenterRechargeEvent) (*BalanceCenterRechargeEvent, error)
	DeleteBalanceCenterRechargeEvent(context.Context, int64) error
	ListBalanceCenterReconciliations(context.Context, BalanceCenterListFilter) (*BalanceCenterPage[BalanceCenterReconciliation], error)
	CreateBalanceCenterReconciliation(context.Context, *BalanceCenterReconciliation) (*BalanceCenterReconciliation, error)
	ListBalanceCenterAlerts(context.Context, BalanceCenterListFilter) (*BalanceCenterPage[BalanceCenterAlertDelivery], error)
}

func (s *BalanceCenterService) adminRepository() (BalanceCenterAdminRepository, error) {
	repository, ok := s.repository.(BalanceCenterAdminRepository)
	if !ok {
		return nil, errors.New("余额中心管理仓储不可用")
	}
	return repository, nil
}

func (s *BalanceCenterService) ListOverview(ctx context.Context) ([]BalanceCenterOverviewItem, error) {
	repository, err := s.adminRepository()
	if err != nil {
		return nil, err
	}
	return repository.ListBalanceCenterOverview(ctx)
}

func (s *BalanceCenterService) ListSites(ctx context.Context) ([]BalanceCenterSite, error) {
	repository, err := s.adminRepository()
	if err != nil {
		return nil, err
	}
	return repository.ListBalanceCenterSites(ctx)
}

func (s *BalanceCenterService) ListSnapshots(ctx context.Context, filter BalanceCenterListFilter) (*BalanceCenterPage[BalanceCenterSnapshot], error) {
	repository, err := s.adminRepository()
	if err != nil {
		return nil, err
	}
	return repository.ListBalanceCenterSnapshots(ctx, normalizeBalanceCenterFilter(filter))
}

func (s *BalanceCenterService) ListManualRows(ctx context.Context) ([]BalanceCenterManualRow, error) {
	repository, err := s.adminRepository()
	if err != nil {
		return nil, err
	}
	return repository.ListBalanceCenterManualRows(ctx)
}

func (s *BalanceCenterService) ReplaceManualRows(ctx context.Context, rows []BalanceCenterManualRow) error {
	repository, err := s.adminRepository()
	if err != nil {
		return err
	}
	for i := range rows {
		rows[i].Label = strings.TrimSpace(rows[i].Label)
		rows[i].Expression = strings.TrimSpace(rows[i].Expression)
		if rows[i].Label == "" || rows[i].Expression == "" || rows[i].Amount < 0 {
			return errors.New("手工基线行内容无效")
		}
		if strings.TrimSpace(rows[i].Source) == "" {
			rows[i].Source = "manual"
		}
		if strings.TrimSpace(rows[i].SourceKey) == "" {
			rows[i].SourceKey = rows[i].Label
		}
	}
	return repository.ReplaceBalanceCenterManualRows(ctx, rows)
}

func (s *BalanceCenterService) ListRechargeEvents(ctx context.Context, filter BalanceCenterListFilter) (*BalanceCenterPage[BalanceCenterRechargeEvent], error) {
	repository, err := s.adminRepository()
	if err != nil {
		return nil, err
	}
	return repository.ListBalanceCenterRechargeEvents(ctx, normalizeBalanceCenterFilter(filter))
}

func (s *BalanceCenterService) GetRechargeSummary(ctx context.Context, filter BalanceCenterListFilter) (*BalanceCenterRechargeSummary, error) {
	repository, err := s.adminRepository()
	if err != nil {
		return nil, err
	}
	return repository.GetBalanceCenterRechargeSummary(ctx, normalizeBalanceCenterFilter(filter))
}

func (s *BalanceCenterService) CreateRechargeEvent(ctx context.Context, event *BalanceCenterRechargeEvent) (*BalanceCenterRechargeEvent, error) {
	if event == nil || event.Amount <= 0 || event.OccurredAt.IsZero() {
		return nil, errors.New("充值记录内容无效")
	}
	if strings.TrimSpace(event.Source) == "" {
		event.Source = "manual"
	}
	if strings.TrimSpace(event.SourceKey) == "" {
		return nil, errors.New("充值记录幂等键不能为空")
	}
	event.SiteLabel = strings.TrimSpace(event.SiteLabel)
	event.Currency = strings.TrimSpace(event.Currency)
	if event.Currency == "" {
		event.Currency = "CNY"
	}
	if event.Currency != "CNY" {
		return nil, errors.New("充值记录币种仅支持 CNY")
	}
	repository, err := s.adminRepository()
	if err != nil {
		return nil, err
	}
	return repository.CreateBalanceCenterRechargeEvent(ctx, event)
}

func (s *BalanceCenterService) DeleteRechargeEvent(ctx context.Context, id int64) error {
	if id <= 0 {
		return errors.New("充值记录 ID 无效")
	}
	repository, err := s.adminRepository()
	if err != nil {
		return err
	}
	return repository.DeleteBalanceCenterRechargeEvent(ctx, id)
}

func (s *BalanceCenterService) ListReconciliations(ctx context.Context, filter BalanceCenterListFilter) (*BalanceCenterPage[BalanceCenterReconciliation], error) {
	repository, err := s.adminRepository()
	if err != nil {
		return nil, err
	}
	return repository.ListBalanceCenterReconciliations(ctx, normalizeBalanceCenterFilter(filter))
}

func (s *BalanceCenterService) CreateReconciliation(ctx context.Context, item *BalanceCenterReconciliation) (*BalanceCenterReconciliation, error) {
	if item == nil || strings.TrimSpace(item.SourceKey) == "" {
		return nil, errors.New("对账记录内容无效")
	}
	if strings.TrimSpace(item.Source) == "" {
		item.Source = "manual"
	}
	repository, err := s.adminRepository()
	if err != nil {
		return nil, err
	}
	return repository.CreateBalanceCenterReconciliation(ctx, item)
}

func (s *BalanceCenterService) ListAlerts(ctx context.Context, filter BalanceCenterListFilter) (*BalanceCenterPage[BalanceCenterAlertDelivery], error) {
	repository, err := s.adminRepository()
	if err != nil {
		return nil, err
	}
	return repository.ListBalanceCenterAlerts(ctx, normalizeBalanceCenterFilter(filter))
}

func normalizeBalanceCenterFilter(filter BalanceCenterListFilter) BalanceCenterListFilter {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 20
	}
	if filter.PageSize > 100 {
		filter.PageSize = 100
	}
	filter.Status = strings.TrimSpace(filter.Status)
	return filter
}
