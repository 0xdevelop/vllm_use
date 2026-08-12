package models

import (
	"context"
	"errors"
	"github.com/0xdevelop/vllm-use/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryBoundariesAndCRUD(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "models")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	local := filepath.Join(root, "one")
	os.Mkdir(local, 0700)
	s, e := store.Open(filepath.Join(base, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	r := New(s, root)
	if _, e = r.AddLocal(context.Background(), base); e == nil {
		t.Fatal("accepted outside path")
	}
	m, e := r.AddLocal(context.Background(), local)
	if e != nil {
		t.Fatal(e)
	}
	h, e := r.AddHuggingFace(context.Background(), "org/model")
	if e != nil {
		t.Fatal(e)
	}
	got, e := r.List(context.Background())
	if e != nil || len(got) != 2 {
		t.Fatalf("list=%v err=%v", got, e)
	}
	if e = r.Delete(context.Background(), h.ID, true); e == nil {
		t.Fatal("deleted HF files")
	}
	if e = r.Delete(context.Background(), m.ID, true); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(local); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("local still exists: %v", e)
	}
}
