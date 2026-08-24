package migrations

import (
	"embed"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// goMigrationVersions holds versions of Go migrations reported by their
// init() functions. SQL migration versions come from embedded filenames.
var goMigrationVersions []int64

// FS contains SQL migrations embedded into the application binary.
//
//go:embed *.sql
var FS embed.FS

// registerThisGoMigration records the calling Go migration file's version,
// derived from its own filename via the call stack (same convention goose
// uses). Each Go migration must call this from its init().
func registerThisGoMigration() {
	_, sourcePath, _, ok := runtime.Caller(1)
	if !ok {
		panic("migrations: cannot resolve caller for Go migration registration")
	}
	version, ok := parseMigrationVersion(filepath.Base(sourcePath))
	if !ok {
		panic("migrations: invalid Go migration filename: " + filepath.Base(sourcePath))
	}
	goMigrationVersions = append(goMigrationVersions, version)
}

// LatestVersion is the newest migration version this binary expects,
// computed from embedded SQL filenames and registered Go migrations so new
// migrations never require editing a shared constant.
func LatestVersion() int64 {
	latest := int64(0)

	sqlEntries, err := fs.ReadDir(FS, ".")
	if err != nil {
		panic("migrations: cannot read embedded migration FS: " + err.Error())
	}
	for _, entry := range sqlEntries {
		if version, ok := parseMigrationVersion(entry.Name()); ok && version > latest {
			latest = version
		}
	}

	for _, version := range goMigrationVersions {
		if version > latest {
			latest = version
		}
	}

	return latest
}

// parseMigrationVersion extracts the leading YYYYMMDDNNNN prefix from a
// migration filename such as 202607290001_usage_token_breakdown_backfill.go.
func parseMigrationVersion(filename string) (int64, bool) {
	prefix, _, found := strings.Cut(filename, "_")
	if !found {
		return 0, false
	}
	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil {
		return 0, false
	}
	return version, true
}
