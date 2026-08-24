package migrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationNoTxContext(upUsageReasoningTTFT, nil)
	registerThisGoMigration()
}

func upUsageReasoningTTFT(ctx context.Context, db *sql.DB) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err := ensureUsageReasoningTTFTColumns(ctx, tx); err != nil {
		return err
	}
	if err := backfillUsageReasoningTTFTColumns(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureUsageReasoningTTFTColumns(ctx context.Context, tx *sql.Tx) error {
	usageColumns, err := tableColumns(ctx, tx, "usage_records")
	if err != nil {
		return err
	}
	if !usageColumns["reasoning_effort"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE usage_records ADD COLUMN reasoning_effort VARCHAR(80)`); err != nil {
			return err
		}
	}
	if !usageColumns["ttft_ms"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE usage_records ADD COLUMN ttft_ms REAL`); err != nil {
			return err
		}
	}
	return nil
}

func backfillUsageReasoningTTFTColumns(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, raw_json FROM usage_records`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type usageReasoningTTFTBackfill struct {
		id              int64
		reasoningEffort *string
		ttftMS          *float64
	}
	updates := []usageReasoningTTFTBackfill{}
	for rows.Next() {
		var id int64
		var rawJSON string
		if err := rows.Scan(&id, &rawJSON); err != nil {
			return err
		}
		var parsed any
		if json.Unmarshal([]byte(rawJSON), &parsed) != nil {
			continue
		}
		updates = append(updates, usageReasoningTTFTBackfill{
			id:              id,
			reasoningEffort: migrationReasoningEffort(parsed),
			ttftMS:          migrationPositiveFloat(migrationFindFirst(parsed, "ttft_ms", "ttftMs")),
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, update := range updates {
		if update.reasoningEffort == nil && update.ttftMS == nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE usage_records
			SET reasoning_effort = COALESCE(NULLIF(reasoning_effort, ''), ?),
			    ttft_ms = COALESCE(ttft_ms, ?)
			WHERE id = ?
		`, nullableMigrationString(update.reasoningEffort), nullableMigrationFloat(update.ttftMS), update.id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE usage_records SET ttft_ms = NULL WHERE ttft_ms <= 0`); err != nil {
		return err
	}
	return nil
}

func migrationReasoningEffort(value any) *string {
	return migrationString(migrationFindFirst(value, "reasoning_effort", "reasoningEffort"))
}

func migrationString(value any) *string {
	switch typed := value.(type) {
	case string:
		normalized := strings.TrimSpace(typed)
		if normalized == "" {
			return nil
		}
		return &normalized
	default:
		return nil
	}
}

func migrationPositiveFloat(value any) *float64 {
	switch typed := value.(type) {
	case float64:
		if typed > 0 {
			return &typed
		}
	case int:
		if typed > 0 {
			value := float64(typed)
			return &value
		}
	case int64:
		if typed > 0 {
			value := float64(typed)
			return &value
		}
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil && parsed > 0 {
			return &parsed
		}
	}
	return nil
}

func nullableMigrationFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}
