-- +goose Up
ALTER TABLE user_api_keys ADD COLUMN allowed_models TEXT NOT NULL DEFAULT '';
