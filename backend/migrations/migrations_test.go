package migrations

import "testing"

// TestLatestVersionMatchesKnownMigrations guards the dynamic derivation:
// if a new migration is added, this test forces the author to confirm the
// derived value advanced (SQL files are picked up automatically; Go
// migrations additionally require the registerThisGoMigration() call).
func TestLatestVersionMatchesKnownMigrations(t *testing.T) {
	const wantLatestVersion = int64(202607290001)

	got := LatestVersion()
	if got != wantLatestVersion {
		t.Fatalf("LatestVersion() = %d, want %d", got, wantLatestVersion)
	}
}

func TestParseMigrationVersion(t *testing.T) {
	testCases := []struct {
		filename    string
		wantVersion int64
		wantOK      bool
	}{
		{filename: "202607290001_usage_token_breakdown_backfill.go", wantVersion: 202607290001, wantOK: true},
		{filename: "202607050001_add_allowed_models.sql", wantVersion: 202607050001, wantOK: true},
		{filename: "202605160001_initial_schema.sql", wantVersion: 202605160001, wantOK: true},
		{filename: "README.md", wantOK: false},
		{filename: "not_a_version_prefix.sql", wantOK: false},
	}

	for _, testCase := range testCases {
		version, ok := parseMigrationVersion(testCase.filename)
		if ok != testCase.wantOK {
			t.Errorf("parseMigrationVersion(%q) ok = %v, want %v", testCase.filename, ok, testCase.wantOK)
			continue
		}
		if version != testCase.wantVersion {
			t.Errorf("parseMigrationVersion(%q) = %d, want %d", testCase.filename, version, testCase.wantVersion)
		}
	}
}
