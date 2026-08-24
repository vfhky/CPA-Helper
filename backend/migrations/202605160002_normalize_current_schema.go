package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationNoTxContext(upNormalizeCurrentSchema, nil)
	registerThisGoMigration()
}

type oldSettingsAccount struct {
	username     string
	passwordHash string
	passwordSalt string
	ok           bool
}

type columnSpec struct {
	name string
	expr string
}

func upNormalizeCurrentSchema(ctx context.Context, db *sql.DB) (err error) {
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer func() {
		if _, restoreErr := db.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`); err == nil && restoreErr != nil {
			err = restoreErr
		}
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	account, err := readOldSettingsAccount(ctx, tx)
	if err != nil {
		return err
	}
	if err := rebuildAppSettings(ctx, tx); err != nil {
		return err
	}
	if err := rebuildUsers(ctx, tx); err != nil {
		return err
	}
	if err := seedUserFromOldSettingsAccount(ctx, tx, account); err != nil {
		return err
	}
	if err := rebuildUserAPIKeys(ctx, tx); err != nil {
		return err
	}
	if err := migrateAPIKeyAliases(ctx, tx); err != nil {
		return err
	}
	if err := rebuildUsageRecords(ctx, tx); err != nil {
		return err
	}
	if err := rebuildModelPrices(ctx, tx); err != nil {
		return err
	}
	if err := rebuildCollectorState(ctx, tx); err != nil {
		return err
	}
	if err := rebuildCodexKeeperAuthStates(ctx, tx); err != nil {
		return err
	}
	if err := rebuildCodexKeeperRuns(ctx, tx); err != nil {
		return err
	}
	if err := rebuildCodexKeeperRunAccounts(ctx, tx); err != nil {
		return err
	}
	if err := dropTableIfExists(ctx, tx, "api_key_aliases"); err != nil {
		return err
	}
	if err := dropTableIfExists(ctx, tx, "alembic_version"); err != nil {
		return err
	}
	if err := createFinalIndexes(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func readOldSettingsAccount(ctx context.Context, tx *sql.Tx) (oldSettingsAccount, error) {
	if !tableExists(ctx, tx, "app_settings") {
		return oldSettingsAccount{}, nil
	}
	cols, err := tableColumns(ctx, tx, "app_settings")
	if err != nil {
		return oldSettingsAccount{}, err
	}
	for _, column := range []string{"account_username", "account_password_hash", "account_password_salt"} {
		if !cols[column] {
			return oldSettingsAccount{}, nil
		}
	}
	row := tx.QueryRowContext(ctx, `
		SELECT account_username, account_password_hash, account_password_salt
		FROM app_settings
		WHERE id = 1
	`)
	var username, passwordHash, passwordSalt sql.NullString
	if err := row.Scan(&username, &passwordHash, &passwordSalt); err != nil {
		if err == sql.ErrNoRows {
			return oldSettingsAccount{}, nil
		}
		return oldSettingsAccount{}, err
	}
	account := oldSettingsAccount{
		username:     strings.TrimSpace(username.String),
		passwordHash: strings.TrimSpace(passwordHash.String),
		passwordSalt: strings.TrimSpace(passwordSalt.String),
	}
	account.ok = account.username != "" && account.passwordHash != "" && account.passwordSalt != ""
	return account, nil
}

func rebuildAppSettings(ctx context.Context, tx *sql.Tx) error {
	cols, _ := tableColumns(ctx, tx, "app_settings")
	specs := []columnSpec{
		{"id", coalesceExpr(cols, "1", "id")},
		{"collector_enabled", coalesceExpr(cols, "0", "collector_enabled")},
		{"cliaproxy_url", coalesceTextExpr(cols, "'http://127.0.0.1:8317'", "cliaproxy_url")},
		{"management_key", coalesceTextExpr(cols, "''", "management_key")},
		{"queue_name", coalesceTextExpr(cols, "'usage'", "queue_name")},
		{"batch_size", coalesceExpr(cols, "100", "batch_size")},
		{"poll_interval_seconds", coalesceExpr(cols, "2.0", "poll_interval_seconds")},
		{"retry_interval_seconds", coalesceExpr(cols, "10.0", "retry_interval_seconds")},
		{"codex_keeper_settings", coalesceTextExpr(cols, "'{}'", "codex_keeper_settings")},
		{"codex_keeper_priority_rules", coalesceTextExpr(cols, "'{}'", "codex_keeper_priority_rules")},
		{"litellm_proxy_enabled", coalesceExpr(cols, "0", "litellm_proxy_enabled")},
		{"litellm_proxy_url", coalesceTextExpr(cols, "''", "litellm_proxy_url")},
		{"model_request_url", coalesceTextExpr(cols, "'http://127.0.0.1:8317'", "model_request_url", "cliaproxy_url")},
		{"session_secret", coalesceTextExpr(cols, "lower(hex(randomblob(48)))", "session_secret")},
		{"created_at", coalesceExpr(cols, "datetime('now')", "created_at")},
		{"updated_at", coalesceExpr(cols, "datetime('now')", "updated_at")},
	}
	return rebuildTable(ctx, tx, "app_settings", createAppSettingsTable, specs, "")
}

func rebuildUsers(ctx context.Context, tx *sql.Tx) error {
	if !tableExists(ctx, tx, "users") {
		return execStatements(ctx, tx, createUsersTable("users"))
	}
	rows, err := selectRows(ctx, tx, `SELECT * FROM users ORDER BY id`)
	if err != nil {
		return err
	}
	if err := dropTableIfExists(ctx, tx, "__goose_users"); err != nil {
		return err
	}
	if err := execStatements(ctx, tx, createUsersTable("__goose_users")); err != nil {
		return err
	}
	used := make(map[string]bool)
	for _, row := range rows {
		id := int64Value(row["id"])
		if id <= 0 {
			continue
		}
		username := firstNonBlank(
			stringValue(row["username"]),
			stringValue(row["name"]),
			fmt.Sprintf("user-%d", id),
		)
		username = uniqueUsername(username, id, used)
		nickname := firstNonBlank(stringValue(row["nickname"]), stringValue(row["remark"]), stringValue(row["name"]))
		_, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO "__goose_users" (
				id, username, password_hash, password_salt, is_admin,
				nickname, disabled_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			id,
			username,
			nullableBlank(stringValue(row["password_hash"])),
			nullableBlank(stringValue(row["password_salt"])),
			boolIntValue(row["is_admin"]),
			nickname,
			nullableBlank(firstNonBlank(stringValue(row["disabled_at"]), stringValue(row["deleted_at"]))),
			firstNonBlank(stringValue(row["created_at"]), nowDB()),
			firstNonBlank(stringValue(row["updated_at"]), nowDB()),
		)
		if err != nil {
			return err
		}
	}
	if err := dropTableIfExists(ctx, tx, "users"); err != nil {
		return err
	}
	return renameTable(ctx, tx, "__goose_users", "users")
}

func seedUserFromOldSettingsAccount(ctx context.Context, tx *sql.Tx, account oldSettingsAccount) error {
	if !account.ok {
		return nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO users (
			username, password_hash, password_salt, is_admin,
			nickname, created_at, updated_at
		) VALUES (?, ?, ?, 1, '', ?, ?)
	`, account.username, account.passwordHash, account.passwordSalt, nowDB(), nowDB())
	return err
}

func rebuildUserAPIKeys(ctx context.Context, tx *sql.Tx) error {
	if !tableExists(ctx, tx, "user_api_keys") {
		return execStatements(ctx, tx, createUserAPIKeysTable("user_api_keys"))
	}
	rows, err := selectRows(ctx, tx, `SELECT * FROM user_api_keys`)
	if err != nil {
		return err
	}
	validUsers, err := userIDSet(ctx, tx)
	if err != nil {
		return err
	}
	if err := dropTableIfExists(ctx, tx, "__goose_user_api_keys"); err != nil {
		return err
	}
	if err := execStatements(ctx, tx, createUserAPIKeysTable("__goose_user_api_keys")); err != nil {
		return err
	}
	for _, row := range rows {
		apiKeyHash := strings.TrimSpace(stringValue(row["api_key_hash"]))
		userID := int64Value(row["user_id"])
		if apiKeyHash == "" || !validUsers[userID] {
			continue
		}
		apiKey := strings.TrimSpace(stringValue(row["api_key"]))
		if apiKey == "" {
			apiKey, _ = usageAPIKey(ctx, tx, apiKeyHash)
		}
		_, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO "__goose_user_api_keys" (
				api_key_hash, user_id, api_key, description, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?)
		`,
			apiKeyHash,
			userID,
			nullableBlank(apiKey),
			firstNonBlank(stringValue(row["description"]), ""),
			firstNonBlank(stringValue(row["created_at"]), nowDB()),
			firstNonBlank(stringValue(row["updated_at"]), nowDB()),
		)
		if err != nil {
			return err
		}
	}
	if err := dropTableIfExists(ctx, tx, "user_api_keys"); err != nil {
		return err
	}
	return renameTable(ctx, tx, "__goose_user_api_keys", "user_api_keys")
}

func migrateAPIKeyAliases(ctx context.Context, tx *sql.Tx) error {
	if !tableExists(ctx, tx, "api_key_aliases") {
		return nil
	}
	cols, err := tableColumns(ctx, tx, "api_key_aliases")
	if err != nil {
		return err
	}
	if !cols["api_key_hash"] || !cols["alias"] {
		return nil
	}
	rows, err := selectRows(ctx, tx, `SELECT api_key_hash, alias, updated_at FROM api_key_aliases`)
	if err != nil {
		return err
	}
	for _, row := range rows {
		apiKeyHash := strings.TrimSpace(stringValue(row["api_key_hash"]))
		username := strings.TrimSpace(stringValue(row["alias"]))
		if apiKeyHash == "" || username == "" {
			continue
		}
		userID, err := ensureAliasUser(ctx, tx, username)
		if err != nil {
			return err
		}
		apiKey, _ := usageAPIKey(ctx, tx, apiKeyHash)
		_, err = tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO user_api_keys (
				api_key_hash, user_id, api_key, description, created_at, updated_at
			) VALUES (?, ?, ?, '', ?, ?)
		`, apiKeyHash, userID, nullableBlank(apiKey), nowDB(), firstNonBlank(stringValue(row["updated_at"]), nowDB()))
		if err != nil {
			return err
		}
	}
	return nil
}

func ensureAliasUser(ctx context.Context, tx *sql.Tx, username string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, username).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO users (username, is_admin, nickname, created_at, updated_at)
		VALUES (?, 0, ?, ?, ?)
	`, username, username, nowDB(), nowDB())
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func rebuildUsageRecords(ctx context.Context, tx *sql.Tx) error {
	cols, _ := tableColumns(ctx, tx, "usage_records")
	specs := []columnSpec{
		{"id", coalesceExpr(cols, "NULL", "id")},
		{"created_at", coalesceExpr(cols, "datetime('now')", "created_at")},
		{"timestamp", coalesceExpr(cols, "datetime('now')", "timestamp", "created_at")},
		{"usage_username", usageUsernameExpr(cols)},
		{"api_key_description", usageDescriptionExpr(cols)},
		{"provider", nullableTextExpr(cols, "provider")},
		{"model", nullableTextExpr(cols, "model")},
		{"reasoning_effort", nullableTextExpr(cols, "reasoning_effort")},
		{"endpoint", nullableTextExpr(cols, "endpoint")},
		{"source", nullableTextExpr(cols, "source")},
		{"request_id", nullableTextExpr(cols, "request_id")},
		{"auth", nullableTextExpr(cols, "auth")},
		{"latency_ms", nullableExpr(cols, "latency_ms")},
		{"ttft_ms", nullableExpr(cols, "ttft_ms")},
		{"failed", coalesceExpr(cols, "0", "failed")},
		{"input_tokens", coalesceExpr(cols, "0", "input_tokens")},
		{"output_tokens", coalesceExpr(cols, "0", "output_tokens")},
		{"cached_tokens", coalesceExpr(cols, "0", "cached_tokens")},
		{"cache_read_tokens", coalesceExpr(cols, "0", "cache_read_tokens")},
		{"cache_creation_tokens", coalesceExpr(cols, "0", "cache_creation_tokens")},
		{"reasoning_tokens", coalesceExpr(cols, "0", "reasoning_tokens")},
		{"total_tokens", coalesceExpr(cols, "0", "total_tokens")},
		{"dedupe_key", coalesceTextExpr(cols, "'migrated-' || COALESCE(CAST(id AS TEXT), lower(hex(randomblob(8))))", "dedupe_key")},
		{"raw_json", coalesceTextExpr(cols, "'{}'", "raw_json")},
	}
	return rebuildTable(ctx, tx, "usage_records", createUsageRecordsTable, specs, "")
}

func usageUsernameExpr(cols map[string]bool) string {
	parts := make([]string, 0, 4)
	if cols["usage_username"] {
		parts = append(parts, `NULLIF("usage_username", '')`)
	}
	if cols["usage_user_account"] {
		parts = append(parts, `NULLIF("usage_user_account", '')`)
	}
	if cols["usage_user_id"] {
		parts = append(parts, `(SELECT users.username FROM users WHERE users.id = usage_records.usage_user_id)`)
	}
	if cols["api_key_hash"] {
		parts = append(parts, `(SELECT users.username FROM user_api_keys JOIN users ON users.id = user_api_keys.user_id WHERE user_api_keys.api_key_hash = usage_records.api_key_hash LIMIT 1)`)
	}
	return coalesceList(parts, "NULL")
}

func usageDescriptionExpr(cols map[string]bool) string {
	parts := make([]string, 0, 2)
	if cols["api_key_description"] {
		parts = append(parts, `NULLIF("api_key_description", '')`)
	}
	if cols["api_key_hash"] {
		parts = append(parts, `(SELECT NULLIF(description, '') FROM user_api_keys WHERE user_api_keys.api_key_hash = usage_records.api_key_hash LIMIT 1)`)
	}
	return coalesceList(parts, "NULL")
}

func rebuildModelPrices(ctx context.Context, tx *sql.Tx) error {
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

func rebuildCollectorState(ctx context.Context, tx *sql.Tx) error {
	cols, _ := tableColumns(ctx, tx, "collector_state")
	specs := []columnSpec{
		{"id", coalesceExpr(cols, "1", "id")},
		{"running", coalesceExpr(cols, "0", "running")},
		{"last_poll_at", nullableExpr(cols, "last_poll_at")},
		{"last_success_at", nullableExpr(cols, "last_success_at")},
		{"last_error", nullableTextExpr(cols, "last_error")},
		{"remote_enabled", nullableExpr(cols, "remote_enabled")},
		{"records_collected", coalesceExpr(cols, "0", "records_collected")},
		{"updated_at", coalesceExpr(cols, "datetime('now')", "updated_at")},
	}
	return rebuildTable(ctx, tx, "collector_state", createCollectorStateTable, specs, "")
}

func rebuildCodexKeeperAuthStates(ctx context.Context, tx *sql.Tx) error {
	cols, _ := tableColumns(ctx, tx, "codex_keeper_auth_states")
	specs := []columnSpec{
		{"auth_name", coalesceTextExpr(cols, "''", "auth_name")},
		{"email", nullableTextExpr(cols, "email")},
		{"account_type", nullableTextExpr(cols, "account_type")},
		{"disabled", coalesceExpr(cols, "0", "disabled", "status_disabled_by_keeper")},
		{"priority", coalesceExpr(cols, "NULL", "priority", "last_priority", "original_priority")},
		{"restore_priority", restorePriorityExpr(cols)},
		{"latest_action", coalesceTextExpr(cols, "NULL", "latest_action", "reason")},
		{"last_error", nullableTextExpr(cols, "last_error")},
		{"last_status_code", nullableExpr(cols, "last_status_code")},
		{"primary_used_percent", nullableExpr(cols, "primary_used_percent")},
		{"secondary_used_percent", nullableExpr(cols, "secondary_used_percent")},
		{"primary_reset_at", nullableExpr(cols, "primary_reset_at")},
		{"secondary_reset_at", nullableExpr(cols, "secondary_reset_at")},
		{"quota_threshold", nullableExpr(cols, "quota_threshold")},
		{"last_checked_at", nullableExpr(cols, "last_checked_at")},
		{"last_healthy_at", nullableExpr(cols, "last_healthy_at")},
		{"created_at", coalesceExpr(cols, "datetime('now')", "created_at")},
		{"updated_at", coalesceExpr(cols, "datetime('now')", "updated_at")},
	}
	where := ""
	if cols["auth_name"] {
		where = `auth_name IS NOT NULL AND auth_name != ''`
	}
	return rebuildTable(ctx, tx, "codex_keeper_auth_states", createCodexKeeperAuthStatesTable, specs, where)
}

func restorePriorityExpr(cols map[string]bool) string {
	if cols["restore_priority"] {
		return quoteIdent("restore_priority")
	}
	if cols["original_priority"] {
		return `CASE WHEN "original_priority" > 20 THEN "original_priority" ELSE NULL END`
	}
	return "NULL"
}

func rebuildCodexKeeperRuns(ctx context.Context, tx *sql.Tx) error {
	cols, _ := tableColumns(ctx, tx, "codex_keeper_runs")
	specs := []columnSpec{
		{"id", coalesceExpr(cols, "NULL", "id")},
		{"mode", coalesceTextExpr(cols, "'manual'", "mode")},
		{"state", coalesceTextExpr(cols, "'completed'", "state")},
		{"detail", nullableTextExpr(cols, "detail")},
		{"started_at", coalesceExpr(cols, "datetime('now')", "started_at")},
		{"finished_at", nullableExpr(cols, "finished_at")},
		{"total", coalesceExpr(cols, "0", "total")},
		{"healthy", coalesceExpr(cols, "0", "healthy")},
		{"status_disabled", coalesceExpr(cols, "0", "status_disabled")},
		{"status_enabled", coalesceExpr(cols, "0", "status_enabled")},
		{"priority_degraded", coalesceExpr(cols, "0", "priority_degraded")},
		{"priority_restored", coalesceExpr(cols, "0", "priority_restored")},
		{"skipped", coalesceExpr(cols, "0", "skipped")},
		{"network_error", coalesceExpr(cols, "0", "network_error")},
		{"created_at", coalesceExpr(cols, "datetime('now')", "created_at")},
		{"updated_at", coalesceExpr(cols, "datetime('now')", "updated_at")},
	}
	return rebuildTable(ctx, tx, "codex_keeper_runs", createCodexKeeperRunsTable, specs, "")
}

func rebuildCodexKeeperRunAccounts(ctx context.Context, tx *sql.Tx) error {
	cols, _ := tableColumns(ctx, tx, "codex_keeper_run_accounts")
	specs := []columnSpec{
		{"id", coalesceExpr(cols, "NULL", "id")},
		{"run_id", coalesceExpr(cols, "NULL", "run_id")},
		{"auth_name", coalesceTextExpr(cols, "''", "auth_name")},
		{"email", nullableTextExpr(cols, "email")},
		{"result", coalesceTextExpr(cols, "'skipped'", "result")},
		{"account_type", nullableTextExpr(cols, "account_type")},
		{"priority", nullableExpr(cols, "priority")},
		{"disabled", nullableExpr(cols, "disabled")},
		{"keeper_action", coalesceTextExpr(cols, "'none'", "keeper_action")},
		{"primary_used_percent", nullableExpr(cols, "primary_used_percent")},
		{"secondary_used_percent", nullableExpr(cols, "secondary_used_percent")},
		{"quota_threshold", nullableExpr(cols, "quota_threshold")},
		{"last_status_code", nullableExpr(cols, "last_status_code")},
		{"last_error", nullableTextExpr(cols, "last_error")},
		{"latest_action", coalesceTextExpr(cols, "NULL", "latest_action", "reason")},
		{"checked_at", coalesceExpr(cols, "datetime('now')", "checked_at")},
		{"created_at", coalesceExpr(cols, "datetime('now')", "created_at")},
	}
	where := ""
	if cols["run_id"] {
		where = `run_id IN (SELECT id FROM codex_keeper_runs)`
	}
	return rebuildTable(ctx, tx, "codex_keeper_run_accounts", createCodexKeeperRunAccountsTable, specs, where)
}

func rebuildTable(ctx context.Context, tx *sql.Tx, table string, create func(string) string, specs []columnSpec, where string) error {
	if !tableExists(ctx, tx, table) {
		return execStatements(ctx, tx, create(table))
	}
	temp := "__goose_" + table
	if err := dropTableIfExists(ctx, tx, temp); err != nil {
		return err
	}
	if err := execStatements(ctx, tx, create(temp)); err != nil {
		return err
	}
	targetColumns := make([]string, 0, len(specs))
	selectExprs := make([]string, 0, len(specs))
	for _, spec := range specs {
		targetColumns = append(targetColumns, quoteIdent(spec.name))
		selectExprs = append(selectExprs, spec.expr)
	}
	if strings.TrimSpace(where) == "" {
		where = "1 = 1"
	}
	insertSQL := fmt.Sprintf(
		`INSERT OR IGNORE INTO %s (%s) SELECT %s FROM %s WHERE %s`,
		quoteIdent(temp),
		strings.Join(targetColumns, ", "),
		strings.Join(selectExprs, ", "),
		quoteIdent(table),
		where,
	)
	if _, err := tx.ExecContext(ctx, insertSQL); err != nil {
		return err
	}
	if err := dropTableIfExists(ctx, tx, table); err != nil {
		return err
	}
	return renameTable(ctx, tx, temp, table)
}

func tableExists(ctx context.Context, tx *sql.Tx, table string) bool {
	var name string
	err := tx.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	return err == nil
}

func tableColumns(ctx context.Context, tx *sql.Tx, table string) (map[string]bool, error) {
	columns := map[string]bool{}
	if !tableExists(ctx, tx, table) {
		return columns, nil
	}
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+quoteIdent(table)+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func indexExists(ctx context.Context, tx *sql.Tx, indexName string) bool {
	var name string
	err := tx.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, indexName).Scan(&name)
	return err == nil
}

func createFinalIndexes(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE INDEX IF NOT EXISTS ix_usage_records_timestamp ON usage_records(timestamp)`,
		`CREATE INDEX IF NOT EXISTS ix_usage_records_usage_username ON usage_records(usage_username)`,
		`CREATE INDEX IF NOT EXISTS ix_usage_records_provider ON usage_records(provider)`,
		`CREATE INDEX IF NOT EXISTS ix_usage_records_model ON usage_records(model)`,
		`CREATE INDEX IF NOT EXISTS ix_usage_records_endpoint ON usage_records(endpoint)`,
		`CREATE INDEX IF NOT EXISTS ix_usage_records_failed ON usage_records(failed)`,
		`CREATE INDEX IF NOT EXISTS ix_user_api_keys_user_id ON user_api_keys(user_id)`,
		`CREATE INDEX IF NOT EXISTS ix_users_disabled_at ON users(disabled_at)`,
		`CREATE INDEX IF NOT EXISTS ix_codex_keeper_auth_states_last_checked_at ON codex_keeper_auth_states(last_checked_at)`,
		`CREATE INDEX IF NOT EXISTS ix_codex_keeper_runs_finished_at ON codex_keeper_runs(finished_at)`,
		`CREATE INDEX IF NOT EXISTS ix_codex_keeper_runs_mode ON codex_keeper_runs(mode)`,
		`CREATE INDEX IF NOT EXISTS ix_codex_keeper_runs_started_at ON codex_keeper_runs(started_at)`,
		`CREATE INDEX IF NOT EXISTS ix_codex_keeper_runs_state ON codex_keeper_runs(state)`,
		`CREATE INDEX IF NOT EXISTS ix_codex_keeper_run_accounts_auth_name ON codex_keeper_run_accounts(auth_name)`,
		`CREATE INDEX IF NOT EXISTS ix_codex_keeper_run_accounts_checked_at ON codex_keeper_run_accounts(checked_at)`,
		`CREATE INDEX IF NOT EXISTS ix_codex_keeper_run_accounts_result ON codex_keeper_run_accounts(result)`,
		`CREATE INDEX IF NOT EXISTS ix_codex_keeper_run_accounts_run_id ON codex_keeper_run_accounts(run_id)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	_ = indexExists(ctx, tx, "ix_user_api_keys_user_id")
	return nil
}

func userIDSet(ctx context.Context, tx *sql.Tx) (map[int64]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

func usageAPIKey(ctx context.Context, tx *sql.Tx, apiKeyHash string) (string, error) {
	if !tableExists(ctx, tx, "usage_records") {
		return "", nil
	}
	cols, err := tableColumns(ctx, tx, "usage_records")
	if err != nil {
		return "", err
	}
	if !cols["api_key_hash"] {
		return "", nil
	}
	if cols["api_key"] {
		var apiKey sql.NullString
		err := tx.QueryRowContext(ctx, `
			SELECT api_key
			FROM usage_records
			WHERE api_key_hash = ? AND api_key IS NOT NULL AND api_key != ''
			ORDER BY timestamp DESC
			LIMIT 1
		`, apiKeyHash).Scan(&apiKey)
		if err == nil && strings.TrimSpace(apiKey.String) != "" {
			return strings.TrimSpace(apiKey.String), nil
		}
		if err != nil && err != sql.ErrNoRows {
			return "", err
		}
	}
	if !cols["raw_json"] {
		return "", nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT raw_json
		FROM usage_records
		WHERE api_key_hash = ?
		ORDER BY timestamp DESC
	`, apiKeyHash)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var rawJSON sql.NullString
		if err := rows.Scan(&rawJSON); err != nil {
			return "", err
		}
		apiKey := apiKeyFromRawJSON(rawJSON.String, apiKeyHash)
		if apiKey != "" {
			return apiKey, nil
		}
	}
	return "", rows.Err()
}

func apiKeyFromRawJSON(rawJSON, apiKeyHash string) string {
	var value any
	if err := json.Unmarshal([]byte(rawJSON), &value); err != nil {
		return ""
	}
	candidate, ok := findFirstString(value, map[string]bool{
		"api_key": true,
		"apiKey":  true,
		"apikey":  true,
		"key":     true,
	})
	if !ok {
		return ""
	}
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || hashAPIKeyForMigration(candidate) != apiKeyHash {
		return ""
	}
	return candidate
}

func findFirstString(value any, keys map[string]bool) (string, bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if keys[key] {
				if text, ok := nested.(string); ok {
					return text, true
				}
			}
		}
		for _, nested := range typed {
			if found, ok := findFirstString(nested, keys); ok {
				return found, true
			}
		}
	case []any:
		for _, nested := range typed {
			if found, ok := findFirstString(nested, keys); ok {
				return found, true
			}
		}
	}
	return "", false
}

func hashAPIKeyForMigration(apiKey string) string {
	normalized := strings.TrimSpace(apiKey)
	if normalized == "" {
		normalized = "unknown"
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func selectRows(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]map[string]any, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var result []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			row[column] = values[i]
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func coalesceExpr(cols map[string]bool, fallback string, names ...string) string {
	parts := make([]string, 0, len(names)+1)
	for _, name := range names {
		if cols[name] {
			parts = append(parts, quoteIdent(name))
		}
	}
	return coalesceList(parts, fallback)
}

func coalesceTextExpr(cols map[string]bool, fallback string, names ...string) string {
	parts := make([]string, 0, len(names)+1)
	for _, name := range names {
		if cols[name] {
			parts = append(parts, "NULLIF("+quoteIdent(name)+", '')")
		}
	}
	return coalesceList(parts, fallback)
}

func coalesceList(parts []string, fallback string) string {
	parts = append(parts, fallback)
	if len(parts) == 1 {
		return parts[0]
	}
	return "COALESCE(" + strings.Join(parts, ", ") + ")"
}

func nullableExpr(cols map[string]bool, name string) string {
	if cols[name] {
		return quoteIdent(name)
	}
	return "NULL"
}

func nullableTextExpr(cols map[string]bool, name string) string {
	if cols[name] {
		return "NULLIF(" + quoteIdent(name) + ", '')"
	}
	return "NULL"
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func dropTableIfExists(ctx context.Context, tx *sql.Tx, table string) error {
	_, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS `+quoteIdent(table))
	return err
}

func renameTable(ctx context.Context, tx *sql.Tx, oldName, newName string) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE `+quoteIdent(oldName)+` RENAME TO `+quoteIdent(newName))
	return err
}

func execStatements(ctx context.Context, tx *sql.Tx, statements ...string) error {
	for _, statement := range statements {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func uniqueUsername(username string, id int64, used map[string]bool) string {
	base := strings.TrimSpace(username)
	if base == "" {
		base = fmt.Sprintf("user-%d", id)
	}
	if !used[base] {
		used[base] = true
		return base
	}
	candidate := fmt.Sprintf("%s-%d", base, id)
	for used[candidate] {
		id++
		candidate = fmt.Sprintf("%s-%d", base, id)
	}
	used[candidate] = true
	return candidate
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func nullableBlank(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	case time.Time:
		return typed.Format("2006-01-02 15:04:05.999999")
	case int64:
		return strconv.FormatInt(typed, 10)
	case int:
		return strconv.Itoa(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		if typed {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprint(typed)
	}
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case []byte:
		parsed, _ := strconv.ParseInt(string(typed), 10, 64)
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	default:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(typed)), 10, 64)
		return parsed
	}
}

func boolIntValue(value any) int {
	switch typed := value.(type) {
	case bool:
		if typed {
			return 1
		}
		return 0
	case int64:
		if typed != 0 {
			return 1
		}
		return 0
	case []byte:
		return boolIntValue(string(typed))
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		if normalized == "1" || normalized == "true" || normalized == "yes" {
			return 1
		}
	}
	return 0
}

func nowDB() string {
	return time.Now().Format("2006-01-02 15:04:05.999999")
}

func createAppSettingsTable(table string) string {
	return `CREATE TABLE ` + quoteIdent(table) + ` (
		id INTEGER PRIMARY KEY,
		collector_enabled BOOLEAN NOT NULL DEFAULT 0,
		cliaproxy_url VARCHAR(500) NOT NULL DEFAULT 'http://127.0.0.1:8317',
		management_key VARCHAR(1000) NOT NULL DEFAULT '',
		queue_name VARCHAR(120) NOT NULL DEFAULT 'usage',
		batch_size INTEGER NOT NULL DEFAULT 100,
		poll_interval_seconds REAL NOT NULL DEFAULT 2.0,
		retry_interval_seconds REAL NOT NULL DEFAULT 10.0,
		codex_keeper_settings TEXT NOT NULL DEFAULT '{}',
		codex_keeper_priority_rules TEXT NOT NULL DEFAULT '{}',
		litellm_proxy_enabled BOOLEAN NOT NULL DEFAULT 0,
		litellm_proxy_url VARCHAR(1000) NOT NULL DEFAULT '',
		model_request_url VARCHAR(1000) NOT NULL DEFAULT 'http://127.0.0.1:8317',
		session_secret VARCHAR(200) NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`
}

func createUsersTable(table string) string {
	return `CREATE TABLE ` + quoteIdent(table) + ` (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username VARCHAR(120) NOT NULL UNIQUE,
		password_hash VARCHAR(200),
		password_salt VARCHAR(64),
		is_admin BOOLEAN NOT NULL DEFAULT 0,
		nickname VARCHAR(240) NOT NULL DEFAULT '',
		disabled_at DATETIME,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`
}

func createUserAPIKeysTable(table string) string {
	return `CREATE TABLE ` + quoteIdent(table) + ` (
		api_key_hash VARCHAR(64) PRIMARY KEY,
		user_id INTEGER NOT NULL,
		api_key VARCHAR(400),
		description VARCHAR(240) NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		FOREIGN KEY(user_id) REFERENCES users(id)
	)`
}

func createUsageRecordsTable(table string) string {
	return `CREATE TABLE ` + quoteIdent(table) + ` (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at DATETIME NOT NULL,
		timestamp DATETIME NOT NULL,
		usage_username VARCHAR(120),
		api_key_description VARCHAR(240),
		provider VARCHAR(120),
		model VARCHAR(180),
		reasoning_effort VARCHAR(80),
		endpoint VARCHAR(240),
		source VARCHAR(120),
		request_id VARCHAR(240),
		auth VARCHAR(120),
		latency_ms REAL,
		ttft_ms REAL,
		failed BOOLEAN NOT NULL DEFAULT 0,
		input_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		cached_tokens INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens INTEGER NOT NULL DEFAULT 0,
		cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
		reasoning_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		dedupe_key VARCHAR(80) NOT NULL UNIQUE,
		raw_json TEXT NOT NULL
	)`
}

func createModelPricesTable(table string) string {
	return `CREATE TABLE ` + quoteIdent(table) + ` (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provider VARCHAR(120) NOT NULL,
		model VARCHAR(180) NOT NULL,
		input_usd_per_million REAL NOT NULL DEFAULT 0,
		output_usd_per_million REAL NOT NULL DEFAULT 0,
		cache_read_usd_per_million REAL NOT NULL DEFAULT 0,
		cache_creation_usd_per_million REAL NOT NULL DEFAULT 0,
		request_usd REAL,
		source VARCHAR(40) NOT NULL DEFAULT 'manual',
		source_model VARCHAR(180),
		auto_synced BOOLEAN NOT NULL DEFAULT 0,
		last_synced_at DATETIME,
		updated_at DATETIME NOT NULL,
		CONSTRAINT uq_model_prices_provider_model UNIQUE (provider, model)
	)`
}

func createCollectorStateTable(table string) string {
	return `CREATE TABLE ` + quoteIdent(table) + ` (
		id INTEGER PRIMARY KEY,
		running BOOLEAN NOT NULL DEFAULT 0,
		last_poll_at DATETIME,
		last_success_at DATETIME,
		last_error TEXT,
		remote_enabled BOOLEAN,
		records_collected INTEGER NOT NULL DEFAULT 0,
		updated_at DATETIME NOT NULL
	)`
}

func createCodexKeeperAuthStatesTable(table string) string {
	return `CREATE TABLE ` + quoteIdent(table) + ` (
		auth_name VARCHAR(500) PRIMARY KEY,
		email VARCHAR(320),
		account_type VARCHAR(80),
		disabled BOOLEAN NOT NULL DEFAULT 0,
		priority INTEGER,
		restore_priority INTEGER,
		latest_action TEXT,
		last_error TEXT,
		last_status_code INTEGER,
		primary_used_percent INTEGER,
		secondary_used_percent INTEGER,
		primary_reset_at DATETIME,
		secondary_reset_at DATETIME,
		quota_threshold INTEGER,
		last_checked_at DATETIME,
		last_healthy_at DATETIME,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`
}

func createCodexKeeperRunsTable(table string) string {
	return `CREATE TABLE ` + quoteIdent(table) + ` (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		mode VARCHAR(20) NOT NULL,
		state VARCHAR(20) NOT NULL,
		detail TEXT,
		started_at DATETIME NOT NULL,
		finished_at DATETIME,
		total INTEGER NOT NULL DEFAULT 0,
		healthy INTEGER NOT NULL DEFAULT 0,
		status_disabled INTEGER NOT NULL DEFAULT 0,
		status_enabled INTEGER NOT NULL DEFAULT 0,
		priority_degraded INTEGER NOT NULL DEFAULT 0,
		priority_restored INTEGER NOT NULL DEFAULT 0,
		skipped INTEGER NOT NULL DEFAULT 0,
		network_error INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`
}

func createCodexKeeperRunAccountsTable(table string) string {
	return `CREATE TABLE ` + quoteIdent(table) + ` (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id INTEGER NOT NULL,
		auth_name VARCHAR(500) NOT NULL,
		email VARCHAR(320),
		result VARCHAR(40) NOT NULL,
		account_type VARCHAR(80),
		priority INTEGER,
		disabled BOOLEAN,
		keeper_action VARCHAR(40) NOT NULL DEFAULT 'none',
		primary_used_percent INTEGER,
		secondary_used_percent INTEGER,
		quota_threshold INTEGER,
		last_status_code INTEGER,
		last_error TEXT,
		latest_action TEXT,
		checked_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL,
		FOREIGN KEY(run_id) REFERENCES codex_keeper_runs(id)
	)`
}
