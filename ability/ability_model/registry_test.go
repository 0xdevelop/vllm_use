package ability_model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xdevelop/vllm-use/db/sqlite"
)

func TestRegistryBoundariesAndCRUD(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "models")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	local := filepath.Join(root, "one")
	os.Mkdir(local, 0700)
	s, e := sqlite.Open(filepath.Join(t.TempDir(), "app.db"))
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
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, e = s.DB.Exec(`INSERT INTO runtime_configs(id,name,model_id,options_json,active,created_at,updated_at) VALUES('active','active',?,'{}',1,?,?)`, m.ID, now, now); e != nil {
		t.Fatal(e)
	}
	if e = r.Delete(context.Background(), m.ID, true); e == nil {
		t.Fatal("deleted active model")
	}
	if _, e = os.Stat(local); e != nil {
		t.Fatalf("active model files moved: %v", e)
	}
	if _, e = s.DB.Exec(`DELETE FROM runtime_configs WHERE id='active'`); e != nil {
		t.Fatal(e)
	}
	if e = r.Delete(context.Background(), m.ID, true); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(local); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("local still exists: %v", e)
	}
}
