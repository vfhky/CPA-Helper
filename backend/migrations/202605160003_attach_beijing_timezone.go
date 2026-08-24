package migrations

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationNoTxContext(upAttachBeijingTimezone, nil)
	registerThisGoMigration()
}

type timezoneColumnGroup struct {
	table   string
	columns []string
}

var beijingTimeLocationForMigration = time.FixedZone("Asia/Shanghai", 8*60*60)

var timezoneColumnGroups = []timezoneColumnGroup{
	{table: "app_settings", columns: []string{"created_at", "updated_at"}},
	{table: "users", columns: []string{"disabled_at", "created_at", "updated_at"}},
	{table: "user_api_keys", columns: []string{"created_at", "updated_at"}},
	{table: "usage_records", columns: []string{"created_at", "timestamp"}},
	{table: "model_prices", columns: []string{"last_synced_at", "updated_at"}},
	{table: "collector_state", columns: []string{"last_poll_at", "last_success_at", "updated_at"}},
	{table: "codex_keeper_auth_states", columns: []string{"primary_reset_at", "secondary_reset_at", "last_checked_at", "last_healthy_at", "created_at", "updated_at"}},
	{table: "codex_keeper_runs", columns: []string{"started_at", "finished_at", "created_at", "updated_at"}},
	{table: "codex_keeper_run_accounts", columns: []string{"checked_at", "created_at"}},
}

func upAttachBeijingTimezone(ctx context.Context, db *sql.DB) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for _, group := range timezoneColumnGroups {
		columns, err := tableColumns(ctx, tx, group.table)
		if err != nil {
			return err
		}
		for _, column := range group.columns {
			if !columns[column] {
				continue
			}
			if err := normalizeTimezoneColumn(ctx, tx, group.table, column); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func normalizeTimezoneColumn(ctx context.Context, tx *sql.Tx, table, column string) error {
	query := `SELECT rowid, CAST(` + quoteIdent(column) + ` AS TEXT) FROM ` + quoteIdent(table) + ` WHERE ` + quoteIdent(column) + ` IS NOT NULL AND TRIM(CAST(` + quoteIdent(column) + ` AS TEXT)) != ''`
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()

	updates := map[int64]string{}
	for rows.Next() {
		var rowID int64
		var raw string
		if err := rows.Scan(&rowID, &raw); err != nil {
			return err
		}
		normalized, ok := normalizeBeijingTimestampForMigration(raw)
		if !ok || normalized == strings.TrimSpace(raw) {
			continue
		}
		updates[rowID] = normalized
	}
	if err := rows.Err(); err != nil {
		return err
	}

	updateSQL := `UPDATE ` + quoteIdent(table) + ` SET ` + quoteIdent(column) + ` = ? WHERE rowid = ?`
	for rowID, value := range updates {
		if _, err := tx.ExecContext(ctx, updateSQL, value, rowID); err != nil {
			return err
		}
	}
	return nil
}

func normalizeBeijingTimestampForMigration(value string) (string, bool) {
	text := strings.TrimSpace(value)
	if text == "" {
		return "", false
	}
	if migrationHasExplicitTimeZone(text) {
		for _, layout := range migrationZonedLayouts() {
			if parsed, err := time.Parse(layout, text); err == nil {
				return formatBeijingTimestampForMigration(parsed), true
			}
		}
	}
	withoutZone := migrationStripTimeZone(text)
	for _, layout := range migrationWallClockLayouts() {
		if parsed, err := time.ParseInLocation(layout, withoutZone, beijingTimeLocationForMigration); err == nil {
			return formatBeijingTimestampForMigration(parsed), true
		}
	}
	return "", false
}

func formatBeijingTimestampForMigration(value time.Time) string {
	return value.In(beijingTimeLocationForMigration).Format("2006-01-02T15:04:05.999999-07:00")
}

func migrationHasExplicitTimeZone(value string) bool {
	text := strings.TrimSpace(value)
	if len(text) <= 10 {
		return false
	}
	tail := text[10:]
	return strings.HasSuffix(tail, "Z") || strings.HasSuffix(tail, "z") ||
		strings.Contains(tail, "+") || strings.Contains(tail, "-")
}

func migrationStripTimeZone(value string) string {
	text := strings.TrimSpace(strings.Replace(value, "T", " ", 1))
	for index := 10; index < len(text); index++ {
		switch text[index] {
		case 'Z', 'z', '+', '-':
			return strings.TrimSpace(text[:index])
		}
	}
	return text
}

func migrationZonedLayouts() []string {
	return []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02T15:04:05.999999999-0700",
		"2006-01-02T15:04:05.999999-0700",
		"2006-01-02T15:04:05-0700",
		"2006-01-02 15:04:05.999999999-0700",
		"2006-01-02 15:04:05.999999-0700",
		"2006-01-02 15:04:05-0700",
	}
}

func migrationWallClockLayouts() []string {
	return []string{
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
}
