package ability_api_key

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

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
