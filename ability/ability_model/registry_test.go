package ability_model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xdevelop/vllm-use/db/sqlite"
)

func TestResolveRuntimeModelRevalidatesManagedDirectory(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	root := filepath.Join(base, "models")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry := New(store, root)

	path := filepath.Join(root, "ready")
	if err = os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	model, err := registry.RegisterLocal(ctx, "ready", path)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.ResolveRuntimeModel(ctx, model.ID)
	if err != nil || resolved.LocalPath != path {
		t.Fatalf("resolve ready model = %#v, %v", resolved, err)
	}

	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err = registry.ResolveRuntimeModel(ctx, model.ID); err == nil {
		t.Fatal("runtime resolver accepted a model path replaced by an escaping symlink")
	}

	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = registry.ResolveRuntimeModel(ctx, model.ID); err == nil {
		t.Fatal("runtime resolver accepted a regular file")
	}
}

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

func TestReconcileDeletionQuarantineRestoresOrPurgesByDatabaseTruth(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "models")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry := New(store, root)

	stage := func(name string) (Model, string, string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "weights.bin"), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		model, err := registry.RegisterLocal(ctx, name, path)
		if err != nil {
			t.Fatal(err)
		}
		qroot := filepath.Join(root, ".quarantine")
		if err = os.MkdirAll(qroot, 0o700); err != nil {
			t.Fatal(err)
		}
		stageDir := filepath.Join(qroot, model.ID)
		if err = os.Mkdir(stageDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err = writeDeletionManifest(stageDir, deletionManifest{ID: model.ID, Original: path}); err != nil {
			t.Fatal(err)
		}
		quarantined := filepath.Join(stageDir, "files")
		if err = os.Rename(path, quarantined); err != nil {
			t.Fatal(err)
		}
		return model, path, stageDir
	}

	_, restorePath, restoreQuarantine := stage("restore-me")
	if err = registry.ReconcileDeletionQuarantine(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(restorePath, "weights.bin")); err != nil {
		t.Fatalf("database-backed model was not restored: %v", err)
	}
	if _, err = os.Stat(restoreQuarantine); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restored quarantine still exists: %v", err)
	}

	preRenameStage := filepath.Join(root, ".quarantine", strings.Repeat("a", 32))
	if err = os.MkdirAll(preRenameStage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = writeDeletionManifest(preRenameStage, deletionManifest{ID: strings.Repeat("a", 32), Original: restorePath}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.ExecContext(ctx, `UPDATE models SET id=? WHERE local_path=?`, strings.Repeat("a", 32), restorePath); err != nil {
		t.Fatal(err)
	}
	if err = registry.ReconcileDeletionQuarantine(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(restorePath, "weights.bin")); err != nil {
		t.Fatalf("pre-rename reconciliation removed original files: %v", err)
	}
	if _, err = os.Stat(preRenameStage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-rename deletion stage still exists: %v", err)
	}

	purged, _, purgeQuarantine := stage("purge-me")
	if _, err = store.DB.ExecContext(ctx, `DELETE FROM models WHERE id=?`, purged.ID); err != nil {
		t.Fatal(err)
	}
	if err = registry.ReconcileDeletionQuarantine(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(purgeQuarantine); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed deletion quarantine still exists: %v", err)
	}
	if _, err = os.Stat(filepath.Join(root, ".quarantine")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty quarantine root still exists: %v", err)
	}
}

func TestReconcileDeletionQuarantineRejectsUnknownEntries(t *testing.T) {
	root := filepath.Join(t.TempDir(), "models")
	qroot := filepath.Join(root, ".quarantine")
	if err := os.MkdirAll(qroot, 0o700); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(qroot, "operator-data")
	if err := os.WriteFile(unknown, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = New(store, root).ReconcileDeletionQuarantine(context.Background()); err == nil {
		t.Fatal("unknown quarantine entry was accepted")
	}
	if got, err := os.ReadFile(unknown); err != nil || string(got) != "keep" {
		t.Fatalf("unknown quarantine entry was changed: %q, %v", got, err)
	}
}
