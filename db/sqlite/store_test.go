package sqlite

import (
	"context"
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

func TestOpenRejectsUnsafeDatabasePaths(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target.db")
		const original = "operator-owned-content"
		if err := os.WriteFile(target, []byte(original), 0644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "state.db")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if store, err := Open(link); err == nil {
			_ = store.Close()
			t.Fatal("database symlink was accepted")
		}
		contents, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(contents) != original {
			t.Fatalf("symlink target was modified: %q", contents)
		}
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0644 {
			t.Fatalf("symlink target permissions changed to %o", info.Mode().Perm())
		}
	})

	t.Run("directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "database")
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if store, err := Open(path); err == nil {
			_ = store.Close()
			t.Fatal("database directory was accepted")
		}
	})
}

func TestOpenTightensExistingDatabasePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.db")
	if err := os.WriteFile(path, nil, 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode %o", info.Mode().Perm())
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
	future := make([]int, len(migrations)+1)
	for i := range future {
		future[i] = i + 1
	}
	for name, versions := range map[string][]int{"hole": {1, 3}, "future": future} {
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
	for i := 0; i < len(migrations)-2; i++ {
		if _, err = db.Exec(migrations[i]); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO schema_migrations VALUES(?,?)`, i+1, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = db.Exec(`INSERT INTO settings(key,value,secret,updated_at) VALUES
		('theme','dark',0,?),
		('hf_token','must-be-removed',0,?),
		('legacy_secret','must-be-removed',1,?)`, now, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO api_requests(id,request_id,method,path,status_code,duration_ms,created_at) VALUES('legacy-row','same-client-id','POST','/v1/responses',200,1,?)`, now); err != nil {
		t.Fatal(err)
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
	var settings, sensitive int
	if err = s.DB.QueryRow(`SELECT COUNT(*), COUNT(*) FILTER (WHERE secret=1 OR lower(key) LIKE '%token%' OR lower(key) LIKE '%secret%') FROM settings`).Scan(&settings, &sensitive); err != nil || settings != 1 || sensitive != 0 {
		t.Fatalf("settings=%d sensitive=%d err=%v", settings, sensitive, err)
	}
	if err = s.RecordRequest(context.Background(), APIRequest{RequestID: "same-client-id", Method: "POST", Path: "/v1/responses", StatusCode: 502}); err != nil {
		t.Fatalf("record duplicate request id after upgrade: %v", err)
	}
	var requests int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM api_requests WHERE request_id='same-client-id'`).Scan(&requests); err != nil || requests != 2 {
		t.Fatalf("preserved duplicate request audits=%d err=%v", requests, err)
	}
}
