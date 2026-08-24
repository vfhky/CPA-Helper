package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationNoTxContext(upModelRequestURL, nil)
	registerThisGoMigration()
}

func upModelRequestURL(ctx context.Context, db *sql.DB) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	cols, err := tableColumns(ctx, tx, "app_settings")
	if err != nil {
		return err
	}
	if !cols["model_request_url"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE app_settings ADD COLUMN model_request_url VARCHAR(1000) NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE app_settings
		SET model_request_url = COALESCE(NULLIF(model_request_url, ''), cliaproxy_url)
		WHERE id = 1
	`); err != nil {
		return err
	}
	return tx.Commit()
}
