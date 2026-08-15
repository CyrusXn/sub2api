package service

import (
	"context"
	"time"
)

const (
	OpsAlertEmailStatusSent        = "sent"
	OpsAlertEmailStatusQueued      = "queued"
	OpsAlertEmailStatusFailed      = "failed"
	OpsAlertEmailStatusQuietHours  = "quiet_hours"
	OpsAlertEmailStatusDisabled    = "disabled"
	OpsAlertEmailStatusRateLimited = "rate_limited"
	OpsAlertEmailStatusSilenced    = "silenced"
)

// OpsAlertEmailDelivery 是不含凭据的运维告警邮件投递审计记录。
type OpsAlertEmailDelivery struct {
	ID             int64      `json:"id"`
	AlertEventID   *int64     `json:"alert_event_id,omitempty"`
	RecipientEmail string     `json:"recipient_email"`
	Status         string     `json:"status"`
	Subject        string     `json:"subject"`
	RuleName       string     `json:"rule_name"`
	Severity       string     `json:"severity"`
	TargetSite     string     `json:"target_site"`
	AccountSummary string     `json:"account_summary"`
	FailureReason  string     `json:"failure_reason"`
	DetailHTML     string     `json:"detail_html"`
	ErrorIDs       []int64    `json:"error_ids"`
	IsDigest       bool       `json:"is_digest"`
	DigestedAt     *time.Time `json:"digested_at,omitempty"`
	SentAt         *time.Time `json:"sent_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type OpsAlertEmailDeliveryInput struct {
	AlertEventID   *int64
	IdempotencyKey string
	RecipientEmail string
	Status         string
	Subject        string
	RuleName       string
	Severity       string
	TargetSite     string
	AccountSummary string
	FailureReason  string
	DetailHTML     string
	ErrorIDs       []int64
	IsDigest       bool
	SentAt         *time.Time
}

type OpsAlertEmailDeliveryFilter struct {
	Status   string
	Page     int
	PageSize int
}

type OpsAlertEmailDeliveryList struct {
	Items    []*OpsAlertEmailDelivery `json:"items"`
	Total    int                      `json:"total"`
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
}

// OpsAlertEmailDeliveryRepository 隔离邮件审计能力，避免继续扩大核心 OpsRepository。
type OpsAlertEmailDeliveryRepository interface {
	InsertAlertEmailDelivery(ctx context.Context, input *OpsAlertEmailDeliveryInput) error
	ListAlertEmailDeliveries(ctx context.Context, filter *OpsAlertEmailDeliveryFilter) (*OpsAlertEmailDeliveryList, error)
	ListPendingQuietAlertEmails(ctx context.Context, since time.Time) ([]*OpsAlertEmailDelivery, error)
	MarkQuietAlertEmailsDigested(ctx context.Context, ids []int64, digestedAt time.Time) error
}
