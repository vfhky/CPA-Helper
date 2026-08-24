package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationNoTxContext(upUserQuotaLifetimeNames, nil)
	registerThisGoMigration()
}

func upUserQuotaLifetimeNames(ctx context.Context, db *sql.DB) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	userColumns, err := tableColumns(ctx, tx, "users")
	if err != nil {
		return err
	}
	if userColumns["quota_total_usd"] && !userColumns["quota_lifetime_usd"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE users RENAME COLUMN quota_total_usd TO quota_lifetime_usd`); err != nil {
			return err
		}
	}

	chargeColumns, err := tableColumns(ctx, tx, "user_quota_charges")
	if err != nil {
		return err
	}
	if chargeColumns["total_deducted_usd"] && !chargeColumns["lifetime_deducted_usd"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE user_quota_charges RENAME COLUMN total_deducted_usd TO lifetime_deducted_usd`); err != nil {
			return err
		}
	}

	return tx.Commit()
}
