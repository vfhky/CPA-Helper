package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationNoTxContext(upModelPriceRequestUSD, nil)
	registerThisGoMigration()
}

func upModelPriceRequestUSD(ctx context.Context, db *sql.DB) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	cols, err := tableColumns(ctx, tx, "model_prices")
	if err != nil {
		return err
	}
	if !cols["request_usd"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE model_prices ADD COLUMN request_usd REAL`); err != nil {
			return err
		}
	}
	return tx.Commit()
}
