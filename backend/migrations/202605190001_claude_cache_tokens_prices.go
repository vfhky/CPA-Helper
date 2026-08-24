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
	goose.AddMigrationNoTxContext(upClaudeCacheTokensPrices, nil)
	registerThisGoMigration()
}

func upClaudeCacheTokensPrices(ctx context.Context, db *sql.DB) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err := ensureUsageCacheTokenColumns(ctx, tx); err != nil {
		return err
	}
	if err := backfillUsageCacheTokenColumns(ctx, tx); err != nil {
		return err
	}
	if err := rebuildModelPricesForCachePrices(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureUsageCacheTokenColumns(ctx context.Context, tx *sql.Tx) error {
	usageColumns, err := tableColumns(ctx, tx, "usage_records")
	if err != nil {
		return err
	}
	if !usageColumns["cache_read_tokens"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE usage_records ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if !usageColumns["cache_creation_tokens"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE usage_records ADD COLUMN cache_creation_tokens INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return nil
}

func backfillUsageCacheTokenColumns(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, raw_json FROM usage_records`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type usageCacheTokenBackfill struct {
		id                  int64
		cacheReadTokens     int
		cacheCreationTokens int
	}
	updates := []usageCacheTokenBackfill{}
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
		updates = append(updates, usageCacheTokenBackfill{
			id:                  id,
			cacheReadTokens:     migrationUsageToken(parsed, "cache_read_tokens", "cache_read_input_tokens"),
			cacheCreationTokens: migrationUsageToken(parsed, "cache_creation_tokens", "cache_creation_input_tokens"),
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, update := range updates {
		if update.cacheReadTokens == 0 && update.cacheCreationTokens == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE usage_records
			SET cache_read_tokens = ?,
			    cache_creation_tokens = ?
			WHERE id = ?
		`, update.cacheReadTokens, update.cacheCreationTokens, update.id); err != nil {
			return err
		}
	}
	return nil
}

func migrationUsageToken(value any, keys ...string) int {
	return migrationTokenInt(migrationFindFirst(value, keys...))
}

func migrationTokenInt(value any) int {
	switch typed := value.(type) {
	case float64:
		if typed < 0 {
			return 0
		}
		return int(typed)
	case int:
		if typed < 0 {
			return 0
		}
		return typed
	case int64:
		if typed < 0 {
			return 0
		}
		return int(typed)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil && parsed > 0 {
			return int(parsed)
		}
	}
	return 0
}

func migrationFindFirst(value any, keys ...string) any {
	keySet := map[string]bool{}
	for _, key := range keys {
		keySet[strings.ToLower(key)] = true
	}
	var walk func(any) any
	walk = func(current any) any {
		switch typed := current.(type) {
		case map[string]any:
			for _, key := range keys {
				if value, ok := typed[key]; ok {
					return value
				}
			}
			for key, child := range typed {
				if keySet[strings.ToLower(key)] {
					return child
				}
				if found := walk(child); found != nil {
					return found
				}
			}
		case []any:
			for _, child := range typed {
				if found := walk(child); found != nil {
					return found
				}
			}
		}
		return nil
	}
	return walk(value)
}

func rebuildModelPricesForCachePrices(ctx context.Context, tx *sql.Tx) error {
	cols, _ := tableColumns(ctx, tx, "model_prices")
	specs := []columnSpec{
		{"id", coalesceExpr(cols, "NULL", "id")},
		{"provider", coalesceTextExpr(cols, "'unknown'", "provider")},
		{"model", coalesceTextExpr(cols, "'unknown'", "model")},
		{"input_usd_per_million", coalesceExpr(cols, "0", "input_usd_per_million")},
		{"output_usd_per_million", coalesceExpr(cols, "0", "output_usd_per_million")},
		{"cache_read_usd_per_million", coalesceExpr(cols, "0", "cache_read_usd_per_million", "cached_usd_per_million")},
		{"cache_creation_usd_per_million", coalesceExpr(cols, "0", "cache_creation_usd_per_million")},
		{"request_usd", nullableExpr(cols, "request_usd")},
		{"source", coalesceTextExpr(cols, "'manual'", "source")},
		{"source_model", nullableTextExpr(cols, "source_model")},
		{"auto_synced", coalesceExpr(cols, "0", "auto_synced")},
		{"last_synced_at", nullableExpr(cols, "last_synced_at")},
		{"updated_at", coalesceExpr(cols, "datetime('now')", "updated_at")},
	}
	return rebuildTable(ctx, tx, "model_prices", createModelPricesTable, specs, "")
}
