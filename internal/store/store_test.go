package store

import (
	"os"
	"path/filepath"
	"testing"
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
