package auth

import (
	"context"
	"github.com/0xdevelop/vllm-use/internal/store"
	"path/filepath"
	"testing"
)

func TestKeyShownAndVerifiedByScope(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
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
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
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
