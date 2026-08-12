package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type tablePreferenceRepository struct {
	db *sql.DB
}

func NewTablePreferenceRepository(db *sql.DB) service.TablePreferenceRepository {
	return &tablePreferenceRepository{db: db}
}

func (r *tablePreferenceRepository) Get(ctx context.Context, userID int64, tableKey string) (*service.TablePreference, error) {
	var preference service.TablePreference
	var hiddenColumns, columnWidths, columnOrder []byte
	err := r.db.QueryRowContext(ctx, `
SELECT user_id, table_key, hidden_columns, column_widths, column_order, schema_version, updated_at
FROM user_table_preferences
WHERE user_id = $1 AND table_key = $2`, userID, tableKey).Scan(
		&preference.UserID,
		&preference.TableKey,
		&hiddenColumns,
		&columnWidths,
		&columnOrder,
		&preference.SchemaVersion,
		&preference.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get table preference: %w", err)
	}
	if err := json.Unmarshal(hiddenColumns, &preference.HiddenColumns); err != nil {
		return nil, fmt.Errorf("decode table preference hidden columns: %w", err)
	}
	if err := json.Unmarshal(columnWidths, &preference.ColumnWidths); err != nil {
		return nil, fmt.Errorf("decode table preference column widths: %w", err)
	}
	if err := json.Unmarshal(columnOrder, &preference.ColumnOrder); err != nil {
		return nil, fmt.Errorf("decode table preference column order: %w", err)
	}
	preference.Exists = true
	return &preference, nil
}

func (r *tablePreferenceRepository) Upsert(ctx context.Context, preference *service.TablePreference) error {
	hiddenColumns, err := json.Marshal(preference.HiddenColumns)
	if err != nil {
		return fmt.Errorf("encode table preference hidden columns: %w", err)
	}
	columnWidths, err := json.Marshal(preference.ColumnWidths)
	if err != nil {
		return fmt.Errorf("encode table preference column widths: %w", err)
	}
	columnOrder, err := json.Marshal(preference.ColumnOrder)
	if err != nil {
		return fmt.Errorf("encode table preference column order: %w", err)
	}
	err = r.db.QueryRowContext(ctx, `
INSERT INTO user_table_preferences (
    user_id, table_key, hidden_columns, column_widths, column_order, schema_version
) VALUES ($1, $2, $3::jsonb, $4::jsonb, $5::jsonb, $6)
ON CONFLICT (user_id, table_key) DO UPDATE SET
    hidden_columns = EXCLUDED.hidden_columns,
    column_widths = EXCLUDED.column_widths,
    column_order = EXCLUDED.column_order,
    schema_version = EXCLUDED.schema_version,
    updated_at = NOW()
RETURNING updated_at`,
		preference.UserID,
		preference.TableKey,
		hiddenColumns,
		columnWidths,
		columnOrder,
		preference.SchemaVersion,
	).Scan(&preference.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert table preference: %w", err)
	}
	return nil
}
