package service

import "time"

// Ops alert rule/event models.
//
// NOTE: These are admin-facing DTOs and intentionally keep JSON naming aligned
// with the existing ops dashboard frontend (backup style).

const (
	OpsAlertStatusFiring         = "firing"
	OpsAlertStatusResolved       = "resolved"
	OpsAlertStatusManualResolved = "manual_resolved"
)

type OpsAlertRule struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`

	Enabled  bool   `json:"enabled"`
	Severity string `json:"severity"`

	MetricType string  `json:"metric_type"`
	Operator   string  `json:"operator"`
	Threshold  float64 `json:"threshold"`

	WindowMinutes    int `json:"window_minutes"`
	SustainedMinutes int `json:"sustained_minutes"`
	CooldownMinutes  int `json:"cooldown_minutes"`

	NotifyEmail bool `json:"notify_email"`

	Filters map[string]any `json:"filters,omitempty"`

	LastTriggeredAt *time.Time `json:"last_triggered_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type OpsAlertEvent struct {
	ID       int64  `json:"id"`
	RuleID   int64  `json:"rule_id"`
	Severity string `json:"severity"`
	Status   string `json:"status"`

	Title       string `json:"title"`
	Description string `json:"description"`

	MetricValue    *float64 `json:"metric_value,omitempty"`
	ThresholdValue *float64 `json:"threshold_value,omitempty"`

	Dimensions map[string]any `json:"dimensions,omitempty"`
	// DedupeKey 仅用于内部冷却去重，不包含未脱敏上游响应。
	DedupeKey string `json:"dedupe_key,omitempty"`

	FiredAt    time.Time  `json:"fired_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`

	EmailSent bool      `json:"email_sent"`
	CreatedAt time.Time `json:"created_at"`
}

// OpsAlertAccountDetail 是账号请求告警的非敏感账号快照，用于邮件和事件详情定位。
type OpsAlertAccountDetail struct {
	ID              int64     `json:"id"`
	AlertEventID    int64     `json:"alert_event_id"`
	AccountID       int64     `json:"account_id"`
	AccountName     string    `json:"account_name"`
	Platform        string    `json:"platform"`
	GroupID         *int64    `json:"group_id,omitempty"`
	GroupName       string    `json:"group_name"`
	Diagnosis       string    `json:"diagnosis"`
	ErrorPhase      string    `json:"error_phase"`
	StatusCode      int       `json:"status_code"`
	OccurredAt      time.Time `json:"occurred_at"`
	ErrorLogID      int64     `json:"error_log_id,omitempty"`
	UserID          *int64    `json:"user_id,omitempty"`
	UserEmail       string    `json:"user_email"`
	APIKeyID        *int64    `json:"api_key_id,omitempty"`
	APIKeyName      string    `json:"api_key_name"`
	RequestID       string    `json:"request_id"`
	ClientRequestID string    `json:"client_request_id"`
	ErrorReason     string    `json:"error_reason"`
	ErrorMessage    string    `json:"error_message"`
	RequestedModel  string    `json:"requested_model"`
	UpstreamModel   string    `json:"upstream_model"`
	CreatedAt       time.Time `json:"created_at"`
}

type OpsAlertSilence struct {
	ID int64 `json:"id"`

	RuleID   int64   `json:"rule_id"`
	Platform string  `json:"platform"`
	GroupID  *int64  `json:"group_id,omitempty"`
	Region   *string `json:"region,omitempty"`

	Until  time.Time `json:"until"`
	Reason string    `json:"reason"`

	CreatedBy *int64    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type OpsAlertEventFilter struct {
	Limit int

	// Cursor pagination (descending by fired_at, then id).
	BeforeFiredAt *time.Time
	BeforeID      *int64

	// Optional filters.
	Status    string
	Severity  string
	EmailSent *bool

	StartTime *time.Time
	EndTime   *time.Time

	// Dimensions filters (best-effort).
	Platform string
	GroupID  *int64
}
