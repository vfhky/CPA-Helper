package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	backendMigrations "cpa-helper/backend/migrations"
)

func TestCheckStartupDoesNotCreateMissingDatabase(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("CPA_HELPER_DATA_DIR", dataDir)

	_, err := CheckStartup(context.Background())
	if !errors.Is(err, ErrDatabaseNotInitialized) {
		t.Fatalf("CheckStartup error = %v, want ErrDatabaseNotInitialized", err)
	}

	dbPath := filepath.Join(dataDir, "db", "cpa_helper.sqlite3")
	if _, statErr := os.Stat(dbPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("startup check created database file or returned unexpected stat error: %v", statErr)
	}
}

func TestMigrateMakesStartupCheckReady(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("CPA_HELPER_DATA_DIR", dataDir)

	report, err := Migrate(context.Background())
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	if report.CurrentVersion != backendMigrations.LatestVersion() {
		t.Fatalf("migration version = %d, want %d", report.CurrentVersion, backendMigrations.LatestVersion())
	}

	check, err := CheckStartup(context.Background())
	if err != nil {
		t.Fatalf("CheckStartup failed after migration: %v", err)
	}
	if check.CurrentVersion != backendMigrations.LatestVersion() {
		t.Fatalf("startup version = %d, want %d", check.CurrentVersion, backendMigrations.LatestVersion())
	}
}

func TestCheckStartupDoesNotCreateVersionTableForUnmigratedDatabase(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("CPA_HELPER_DATA_DIR", dataDir)
	dbDir := filepath.Join(dataDir, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "cpa_helper.sqlite3")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = CheckStartup(context.Background())
	if !errors.Is(err, ErrDatabaseNotInitialized) {
		t.Fatalf("CheckStartup error = %v, want ErrDatabaseNotInitialized", err)
	}

	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("goose_db_version table count = %d, want 0", count)
	}
}

func TestReadyEndpointReportsMigrationVersion(t *testing.T) {
	t.Setenv("CPA_HELPER_DATA_DIR", t.TempDir())
	app, err := NewWithOptions(context.Background(), NewOptions{Migrate: true})
	if err != nil {
		t.Fatalf("NewWithOptions failed: %v", err)
	}
	defer app.Close()

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/ready", nil)
	recorder := httptest.NewRecorder()
	app.Routes().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("ready status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
