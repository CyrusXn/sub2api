package service

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrTablePreferenceInvalid = errors.New("invalid table preference")

// TablePreference 保存管理员在列表页调整后的列布局。
type TablePreference struct {
	UserID        int64              `json:"-"`
	TableKey      string             `json:"table_key"`
	Exists        bool               `json:"exists"`
	HiddenColumns []string           `json:"hidden_columns"`
	ColumnWidths  map[string]float64 `json:"column_widths"`
	ColumnOrder   []string           `json:"column_order"`
	SchemaVersion int                `json:"schema_version"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

type TablePreferenceRepository interface {
	Get(ctx context.Context, userID int64, tableKey string) (*TablePreference, error)
	Upsert(ctx context.Context, preference *TablePreference) error
}

type TablePreferenceService struct {
	repo TablePreferenceRepository
}

func NewTablePreferenceService(repo TablePreferenceRepository) *TablePreferenceService {
	return &TablePreferenceService{repo: repo}
}

func NormalizeTablePreferenceKey(value string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(value))
	switch key {
	case "users", "groups", "accounts":
		return key, true
	default:
		return "", false
	}
}

func (s *TablePreferenceService) Get(ctx context.Context, userID int64, tableKey string) (*TablePreference, error) {
	key, ok := NormalizeTablePreferenceKey(tableKey)
	if !ok || userID <= 0 {
		return nil, ErrTablePreferenceInvalid
	}
	preference, err := s.repo.Get(ctx, userID, key)
	if err != nil || preference != nil {
		return preference, err
	}
	return &TablePreference{
		TableKey:      key,
		Exists:        false,
		HiddenColumns: []string{},
		ColumnWidths:  map[string]float64{},
		ColumnOrder:   []string{},
		SchemaVersion: 1,
	}, nil
}

func (s *TablePreferenceService) Save(ctx context.Context, userID int64, preference *TablePreference) (*TablePreference, error) {
	if preference == nil || userID <= 0 {
		return nil, ErrTablePreferenceInvalid
	}
	key, ok := NormalizeTablePreferenceKey(preference.TableKey)
	if !ok {
		return nil, ErrTablePreferenceInvalid
	}
	preference.UserID = userID
	preference.TableKey = key
	preference.Exists = true
	if preference.HiddenColumns == nil {
		preference.HiddenColumns = []string{}
	}
	if preference.ColumnWidths == nil {
		preference.ColumnWidths = map[string]float64{}
	}
	if preference.ColumnOrder == nil {
		preference.ColumnOrder = []string{}
	}
	if preference.SchemaVersion <= 0 {
		preference.SchemaVersion = 1
	}
	if err := s.repo.Upsert(ctx, preference); err != nil {
		return nil, err
	}
	return preference, nil
}
