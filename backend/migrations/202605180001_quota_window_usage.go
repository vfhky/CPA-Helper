package migrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationNoTxContext(upQuotaWindowUsage, nil)
	registerThisGoMigration()
}

var migrationUsageEmailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)

func upQuotaWindowUsage(ctx context.Context, db *sql.DB) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err := ensureQuotaWindowUsageColumns(ctx, tx); err != nil {
		return err
	}
	if err := backfillUsageAccountColumns(ctx, tx); err != nil {
		return err
	}
	if err := createQuotaWindowUsageIndexes(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureQuotaWindowUsageColumns(ctx context.Context, tx *sql.Tx) error {
	usageColumns, err := tableColumns(ctx, tx, "usage_records")
	if err != nil {
		return err
	}
	if !usageColumns["source_account"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE usage_records ADD COLUMN source_account VARCHAR(320)`); err != nil {
			return err
		}
	}
	if !usageColumns["auth_index"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE usage_records ADD COLUMN auth_index VARCHAR(500)`); err != nil {
			return err
		}
	}

	stateColumns, err := tableColumns(ctx, tx, "codex_keeper_auth_states")
	if err != nil {
		return err
	}
	if !stateColumns["primary_window_seconds"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE codex_keeper_auth_states ADD COLUMN primary_window_seconds INTEGER`); err != nil {
			return err
		}
	}
	if !stateColumns["secondary_window_seconds"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE codex_keeper_auth_states ADD COLUMN secondary_window_seconds INTEGER`); err != nil {
			return err
		}
	}
	return nil
}

func backfillUsageAccountColumns(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, source, raw_json
		FROM usage_records
		WHERE source_account IS NULL OR TRIM(source_account) = ''
		   OR auth_index IS NULL OR TRIM(auth_index) = ''
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type usageAccountBackfill struct {
		id            int64
		sourceAccount *string
		authIndex     *string
	}
	updates := []usageAccountBackfill{}
	for rows.Next() {
		var id int64
		var source sql.NullString
		var rawJSON string
		if err := rows.Scan(&id, &source, &rawJSON); err != nil {
			return err
		}
		var sourceText *string
		if source.Valid {
			value := source.String
			sourceText = &value
		}
		updates = append(updates, usageAccountBackfill{
			id:            id,
			sourceAccount: migrationSourceAccount(sourceText),
			authIndex:     migrationAuthIndex(rawJSON),
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, update := range updates {
		if _, err := tx.ExecContext(ctx, `
			UPDATE usage_records
			SET source_account = COALESCE(NULLIF(source_account, ''), ?),
			    auth_index = COALESCE(NULLIF(auth_index, ''), ?)
			WHERE id = ?
		`, nullableMigrationString(update.sourceAccount), nullableMigrationString(update.authIndex), update.id); err != nil {
			return err
		}
	}
	return nil
}

func createQuotaWindowUsageIndexes(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE INDEX IF NOT EXISTS ix_usage_records_timestamp ON usage_records(timestamp)`,
		`CREATE INDEX IF NOT EXISTS ix_usage_records_source_account_timestamp ON usage_records(source_account, timestamp)`,
		`CREATE INDEX IF NOT EXISTS ix_usage_records_auth_index_timestamp ON usage_records(auth_index, timestamp)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrationSourceAccount(source *string) *string {
	if source == nil {
		return nil
	}
	if match := migrationUsageEmailPattern.FindString(*source); match != "" {
		normalized := strings.ToLower(strings.TrimSpace(match))
		return &normalized
	}
	return nil
}

func migrationAuthIndex(rawJSON string) *string {
	for _, field := range []string{"auth_index", "authIndex", "index", "auth_name", "authName", "account_id", "accountId"} {
		if value := migrationJSONStringField(rawJSON, field); value != nil {
			return value
		}
	}
	return nil
}

func migrationJSONStringField(rawJSON, fieldName string) *string {
	var payload map[string]any
	if json.Unmarshal([]byte(rawJSON), &payload) != nil {
		return nil
	}
	value, ok := payload[fieldName]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case string:
		normalized := strings.TrimSpace(typed)
		if normalized == "" {
			return nil
		}
		return &normalized
	case float64:
		text := strconv.FormatFloat(typed, 'f', -1, 64)
		return &text
	case bool:
		text := strconv.FormatBool(typed)
		return &text
	default:
		return nil
	}
}

func nullableMigrationString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
