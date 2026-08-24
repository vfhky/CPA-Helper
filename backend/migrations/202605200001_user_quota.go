package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationNoTxContext(upUserQuota, nil)
	registerThisGoMigration()
}

func upUserQuota(ctx context.Context, db *sql.DB) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	cols, err := tableColumns(ctx, tx, "users")
	if err != nil {
		return err
	}
	userColumns := []struct {
		name string
		sql  string
	}{
		{"quota_lifetime_usd", `ALTER TABLE users ADD COLUMN quota_lifetime_usd REAL`},
		{"quota_monthly_usd", `ALTER TABLE users ADD COLUMN quota_monthly_usd REAL`},
		{"quota_started_at", `ALTER TABLE users ADD COLUMN quota_started_at DATETIME`},
		{"quota_month", `ALTER TABLE users ADD COLUMN quota_month VARCHAR(7) NOT NULL DEFAULT ''`},
		{"quota_month_used_usd", `ALTER TABLE users ADD COLUMN quota_month_used_usd REAL NOT NULL DEFAULT 0`},
		{"quota_paused_at", `ALTER TABLE users ADD COLUMN quota_paused_at DATETIME`},
		{"quota_pause_reason", `ALTER TABLE users ADD COLUMN quota_pause_reason TEXT`},
		{"quota_sync_error", `ALTER TABLE users ADD COLUMN quota_sync_error TEXT`},
		{"quota_unpriced_records", `ALTER TABLE users ADD COLUMN quota_unpriced_records INTEGER NOT NULL DEFAULT 0`},
	}
	for _, column := range userColumns {
		if !cols[column.name] {
			if _, err := tx.ExecContext(ctx, column.sql); err != nil {
				return err
			}
		}
	}

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS user_quota_charges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			usage_record_id INTEGER NOT NULL UNIQUE,
			user_id INTEGER NOT NULL,
			usage_username VARCHAR(120) NOT NULL,
			amount_usd REAL NOT NULL DEFAULT 0,
			monthly_deducted_usd REAL NOT NULL DEFAULT 0,
			lifetime_deducted_usd REAL NOT NULL DEFAULT 0,
			unpriced BOOLEAN NOT NULL DEFAULT 0,
			quota_month VARCHAR(7) NOT NULL,
			created_at DATETIME NOT NULL,
			FOREIGN KEY(usage_record_id) REFERENCES usage_records(id),
			FOREIGN KEY(user_id) REFERENCES users(id)
		)
	`); err != nil {
		return err
	}
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS ix_user_quota_charges_user_id ON user_quota_charges(user_id)`,
		`CREATE INDEX IF NOT EXISTS ix_user_quota_charges_created_at ON user_quota_charges(created_at)`,
		`CREATE INDEX IF NOT EXISTS ix_user_quota_charges_quota_month ON user_quota_charges(quota_month)`,
	}
	for _, statement := range indexes {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}
