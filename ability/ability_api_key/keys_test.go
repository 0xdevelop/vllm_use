package ability_api_key

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xdevelop/vllm-use/db/sqlite"
)

func TestKeyShownAndVerifiedByScope(t *testing.T) {
	s, e := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	m := New(s)
	k, secret, e := m.Create(context.Background(), []string{"inference"})
	if e != nil {
		t.Fatal(e)
	}
	if secret == "" || k.Prefix == secret {
		t.Fatal("bad secret/prefix")
	}
	var stored string
	if e = s.DB.QueryRow(`SELECT hex(hash) FROM api_keys WHERE id=?`, k.ID).Scan(&stored); e != nil {
		t.Fatal(e)
	}
	if stored == secret {
		t.Fatal("secret stored")
	}
	if _, e = m.Verify(context.Background(), secret, "inference"); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Verify(context.Background(), secret, "mcp.models"); e == nil {
		t.Fatal("scope accepted")
	}
	if _, e = m.Verify(context.Background(), secret+"x", "inference"); e == nil {
		t.Fatal("bad secret accepted")
	}
}

func TestKeyCRUDAndAdminScopeSemantics(t *testing.T) {
	s, e := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	m := New(s)
	k, secret, e := m.CreateNamed(context.Background(), "operator", []string{"admin.write"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Verify(context.Background(), secret, "admin.read"); e != nil {
		t.Fatal(e)
	}
	keys, e := m.List(context.Background())
	if e != nil || len(keys) != 1 || keys[0].Name != "operator" {
		t.Fatalf("keys=%v err=%v", keys, e)
	}
	if e = m.SetEnabled(context.Background(), k.ID, false); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Verify(context.Background(), secret, "admin.write"); e == nil {
		t.Fatal("disabled key verified")
	}
	if e = m.Delete(context.Background(), k.ID); e != nil {
		t.Fatal(e)
	}
}

func TestMCPAdminScopeDoesNotEscapeTheMCPControlPlane(t *testing.T) {
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := New(s)
	_, secret, err := m.Create(context.Background(), []string{"mcp.admin"})
	if err != nil {
		t.Fatal(err)
	}

	for _, scope := range []string{"mcp.read", "mcp.models", "mcp.runtime", "mcp.admin"} {
		if _, err = m.Verify(context.Background(), secret, scope); err != nil {
			t.Fatalf("mcp.admin did not imply %q: %v", scope, err)
		}
	}
	for _, scope := range []string{"inference", "admin.read", "admin.write"} {
		if _, err = m.Verify(context.Background(), secret, scope); !errors.Is(err, ErrInsufficientScope) {
			t.Fatalf("mcp.admin escaped into %q: %v", scope, err)
		}
	}
}

func TestVerifyRejectsMalformedSecretsBeforeDatabaseWork(t *testing.T) {
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(s)
	_, secret, err := m.Create(context.Background(), []string{"inference"})
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 51 || !strings.HasPrefix(secret, "vu_") {
		t.Fatalf("generated secret has unexpected format: length=%d", len(secret))
	}
	for _, r := range secret[3:] {
		if !strings.ContainsRune(alphabet, r) {
			t.Fatalf("generated secret contains invalid character %q", r)
		}
	}

	// Closing the store proves malformed credentials are rejected at the cheap
	// parser boundary, before a SQLite lookup or expensive scrypt derivation.
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	malformed := []string{
		"vu_short",
		secret + "x",
		secret[:len(secret)-1],
		secret[:11] + "_" + secret[12:],
		"xx_" + secret[3:],
		secret[:11] + strings.Repeat("a", 1<<20),
	}
	for _, candidate := range malformed {
		if _, err = m.Verify(context.Background(), candidate, "inference"); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("malformed credential length=%d returned %v, want ErrInvalidKey", len(candidate), err)
		}
	}
}

func TestVerifyDoesNotRequireSpareDatabaseConnection(t *testing.T) {
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := New(s)
	_, secret, err := m.Create(context.Background(), []string{"inference"})
	if err != nil {
		t.Fatal(err)
	}

	// Production uses a small pool. Constraining this test to one connection
	// catches a verifier that retains its SELECT cursor while trying to UPDATE
	// last_used_at: that implementation blocks waiting for a connection it owns.
	s.DB.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	key, err := m.Verify(ctx, secret, "inference")
	if err != nil {
		t.Fatalf("verify with one database connection: %v", err)
	}
	if key.LastUsedAt == nil {
		t.Fatal("successful verification did not publish last-used time")
	}
}

func TestKeyReadsRejectCorruptPersistedAuthorizationMetadata(t *testing.T) {
	tests := []struct {
		name       string
		column     string
		value      any
		verifyOnly bool
	}{
		{name: "unknown scope", column: "scopes", value: "inference,root"},
		{name: "empty scope", column: "scopes", value: "inference,"},
		{name: "duplicate scope", column: "scopes", value: "inference,inference"},
		{name: "invalid enabled flag", column: "enabled", value: 2},
		{name: "invalid name", column: "name", value: "line\nbreak"},
		{name: "invalid prefix", column: "prefix", value: "vu_bad-bad"},
		{name: "short salt", column: "salt", value: []byte("short"), verifyOnly: true},
		{name: "short hash", column: "hash", value: []byte("short"), verifyOnly: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := sqlite.Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			m := New(s)
			key, secret, err := m.Create(context.Background(), []string{"inference"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.DB.Exec(`UPDATE api_keys SET `+tc.column+`=? WHERE id=?`, tc.value, key.ID); err != nil {
				t.Fatal(err)
			}

			if _, err = m.Verify(context.Background(), secret, "inference"); err == nil {
				t.Fatalf("Verify accepted corrupt %s", tc.column)
			}
			var lastUsed any
			if err = s.DB.QueryRow(`SELECT last_used_at FROM api_keys WHERE id=?`, key.ID).Scan(&lastUsed); err != nil {
				t.Fatal(err)
			}
			if lastUsed != nil {
				t.Fatalf("rejected key updated last_used_at to %v", lastUsed)
			}
			if _, err = m.List(context.Background()); err == nil && !tc.verifyOnly {
				t.Fatalf("List accepted corrupt %s", tc.column)
			}
		})
	}
}

func TestCreateRejectsUnsafeKeyNamesWithoutPersistence(t *testing.T) {
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := New(s)
	for _, name := range []string{"line\nbreak", string([]byte{'b', 'a', 'd', 0xff}), strings.Repeat("n", 101)} {
		if _, _, err = m.CreateNamed(context.Background(), name, []string{"inference"}); err == nil {
			t.Fatalf("accepted unsafe key name %q", name)
		}
	}
	var count int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM api_keys`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected names persisted keys: count=%d err=%v", count, err)
	}
}

func TestKeyReadsRejectCorruptPersistedTimestamps(t *testing.T) {
	tests := []struct {
		name   string
		column string
		read   func(*Manager, string) error
	}{
		{
			name:   "verify created time",
			column: "created_at",
			read: func(m *Manager, secret string) error {
				_, err := m.Verify(context.Background(), secret, "inference")
				return err
			},
		},
		{
			name:   "verify last-used time",
			column: "last_used_at",
			read: func(m *Manager, secret string) error {
				_, err := m.Verify(context.Background(), secret, "inference")
				return err
			},
		},
		{
			name:   "list created time",
			column: "created_at",
			read: func(m *Manager, _ string) error {
				_, err := m.List(context.Background())
				return err
			},
		},
		{
			name:   "list last-used time",
			column: "last_used_at",
			read: func(m *Manager, _ string) error {
				_, err := m.List(context.Background())
				return err
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := sqlite.Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			m := New(s)
			key, secret, err := m.Create(context.Background(), []string{"inference"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.DB.Exec(`UPDATE api_keys SET `+tc.column+`='not-a-time' WHERE id=?`, key.ID); err != nil {
				t.Fatal(err)
			}
			if err = tc.read(m, secret); err == nil {
				t.Fatalf("corrupt %s was silently accepted", tc.column)
			}
		})
	}
}
