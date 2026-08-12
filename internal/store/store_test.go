package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenMigratesAndSecures(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state", "db.sqlite")
	s, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var n int
	if e = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); e != nil || n != len(migrations) {
		t.Fatalf("migrations=%d err=%v", n, e)
	}
	st, e := os.Stat(p)
	if e != nil {
		t.Fatal(e)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("mode %o", st.Mode().Perm())
	}
}

func seedMigrationHistory(t *testing.T, path string, versions ...int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, v := range versions {
		if _, err = db.Exec(`INSERT INTO schema_migrations VALUES(?,?)`, v, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenRejectsMigrationHoleAndFuture(t *testing.T) {
	for name, versions := range map[string][]int{"hole": {1, 3}, "future": {1, 2, 3, 4, 5, 6}} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "db.sqlite")
			seedMigrationHistory(t, p, versions...)
			if s, err := Open(p); err == nil {
				s.Close()
				t.Fatal("invalid history accepted")
			}
		})
	}
}

func TestOpenSafelyEncodesFilenameAndUpgrades(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state ?# db.sqlite")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil || n != len(migrations) {
		t.Fatalf("versions=%d err=%v", n, err)
	}
	var oldIndex int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='api_keys_prefix_idx'`).Scan(&oldIndex); err != nil || oldIndex != 0 {
		t.Fatalf("redundant index count=%d err=%v", oldIndex, err)
	}
}

func TestOpenUpgradesExistingSchema(t *testing.T) {
	p := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(migrations)-1; i++ {
		if _, err = db.Exec(migrations[i]); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO schema_migrations VALUES(?,?)`, i+1, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version int
	if err = s.DB.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != len(migrations) {
		t.Fatalf("version=%d err=%v", version, err)
	}
	var oldIndex int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='api_keys_prefix_idx'`).Scan(&oldIndex); err != nil || oldIndex != 0 {
		t.Fatalf("redundant index count=%d err=%v", oldIndex, err)
	}
}
