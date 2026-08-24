-- +goose Up
ALTER TABLE codex_keeper_auth_states ADD COLUMN auth_index VARCHAR(500);

CREATE INDEX IF NOT EXISTS ix_codex_keeper_auth_states_auth_index
	ON codex_keeper_auth_states(auth_index);

-- +goose Down
DROP INDEX IF EXISTS ix_codex_keeper_auth_states_auth_index;

ALTER TABLE codex_keeper_auth_states DROP COLUMN auth_index;
