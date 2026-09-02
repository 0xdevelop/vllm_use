package ability_model

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/0xdevelop/vllm-use/api/api_supported_methods"
	"github.com/0xdevelop/vllm-use/db/sqlite"
)

const MethodList = "models.list"

var currentRegistry *Registry

func Setup(registry *Registry) { currentRegistry = registry }

func LoadAPIMethods() {
	api_supported_methods.AddMethod(&api_supported_methods.SupportedMethod{
		Name:        MethodList,
		Description: "列出已注册模型",
		Scope:       "mcp.models",
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Execute: func(ctx context.Context, _ interface{}) (interface{}, error) {
			if currentRegistry == nil {
				return nil, errors.New("model ability is not initialized")
			}
			return currentRegistry.List(ctx)
		},
	})
}

type Model struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	Source     string    `json:"source"`
	Repository string    `json:"repository"`
	Revision   string    `json:"revision"`
	LocalPath  string    `json:"local_path"`
	Status     string    `json:"status"`
	SizeBytes  int64     `json:"size_bytes"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
type Registry struct {
	store *sqlite.Store
	root  string
	mu    sync.Mutex
}

func New(s *sqlite.Store, root string) *Registry {
	return &Registry{store: s, root: filepath.Clean(root)}
}
func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func validateRepo(repo string) error {
	repo = strings.TrimSpace(repo)
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.HasPrefix(repo, "-") || strings.ContainsAny(repo, "\\\x00\n\r\t ") || strings.Contains(repo, "..") {
		return errors.New("invalid repository; expected owner/name")
	}
	return nil
}
func (r *Registry) AddHuggingFace(ctx context.Context, repo string) (Model, error) {
	return r.RegisterHuggingFace(ctx, repo, "")
}
func (r *Registry) RegisterHuggingFace(ctx context.Context, repo, revision string) (Model, error) {
	repo, revision = strings.TrimSpace(repo), strings.TrimSpace(revision)
	if err := validateRepo(repo); err != nil {
		return Model{}, err
	}
	if strings.HasPrefix(revision, "-") || strings.ContainsAny(revision, "\x00\n\r") {
		return Model{}, errors.New("invalid revision")
	}
	return r.add(ctx, Model{Name: filepath.Base(repo), Kind: "huggingface", Source: repo, Repository: repo, Revision: revision, Status: "registered"})
}
func (r *Registry) AddLocal(ctx context.Context, path string) (Model, error) {
	return r.RegisterLocal(ctx, filepath.Base(path), path)
}
func (r *Registry) RegisterLocal(ctx context.Context, name, path string) (Model, error) {
	p, err := r.safeExisting(path)
	if err != nil {
		return Model{}, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return Model{}, fmt.Errorf("stat model: %w", err)
	}
	if !st.IsDir() {
		return Model{}, errors.New("model path must be a directory")
	}
	sz, err := dirSize(p)
	if err != nil {
		return Model{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = filepath.Base(p)
	}
	return r.add(ctx, Model{Name: name, Kind: "local", Source: p, LocalPath: p, SizeBytes: sz, Status: "ready"})
}
func (r *Registry) add(ctx context.Context, m Model) (Model, error) {
	var err error
	m.ID, err = newID()
	if err != nil {
		return Model{}, err
	}
	m.CreatedAt = time.Now().UTC()
	m.UpdatedAt = m.CreatedAt
	_, err = r.store.DB.ExecContext(ctx, `INSERT INTO models(id,kind,source,local_path,created_at,name,repository,revision,size_bytes,status,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, m.ID, m.Kind, m.Source, nullString(m.LocalPath), stamp(m.CreatedAt), m.Name, m.Repository, m.Revision, m.SizeBytes, m.Status, stamp(m.UpdatedAt))
	if err != nil {
		return Model{}, fmt.Errorf("register model: %w", err)
	}
	return m, nil
}
func (r *Registry) Get(ctx context.Context, id string) (Model, error) {
	return scanModel(r.store.DB.QueryRowContext(ctx, selectModel+` WHERE id=?`, id))
}

// ResolveRuntimeModel revalidates the persisted model path at the last domain
// boundary before a runtime launch. A ready database row is not sufficient:
// files may have disappeared or a local path may have been replaced after
// registration.
func (r *Registry) ResolveRuntimeModel(ctx context.Context, id string) (Model, error) {
	m, err := r.Get(ctx, id)
	if err != nil {
		return Model{}, err
	}
	if m.Status != "ready" || m.LocalPath == "" {
		return Model{}, errors.New("model is not ready for runtime")
	}
	info, err := os.Lstat(m.LocalPath)
	if err != nil {
		return Model{}, fmt.Errorf("inspect runtime model path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Model{}, errors.New("runtime model path must not be a symlink")
	}
	if !info.IsDir() {
		return Model{}, errors.New("runtime model path must be a directory")
	}
	resolved, err := r.safeExisting(m.LocalPath)
	if err != nil {
		return Model{}, err
	}
	if resolved != filepath.Clean(m.LocalPath) {
		return Model{}, errors.New("runtime model path changed after registration")
	}
	m.LocalPath = resolved
	return m, nil
}

const selectModel = `SELECT id,name,kind,source,repository,revision,COALESCE(local_path,''),size_bytes,status,created_at,updated_at FROM models`

type scanner interface{ Scan(...any) error }

func scanModel(row scanner) (Model, error) {
	var m Model
	var c, u string
	err := row.Scan(&m.ID, &m.Name, &m.Kind, &m.Source, &m.Repository, &m.Revision, &m.LocalPath, &m.SizeBytes, &m.Status, &c, &u)
	if errors.Is(err, sql.ErrNoRows) {
		return Model{}, sqlite.ErrNotFound
	}
	if err != nil {
		return Model{}, fmt.Errorf("read model: %w", err)
	}
	m.CreatedAt, err = time.Parse(time.RFC3339Nano, c)
	if err != nil {
		return Model{}, fmt.Errorf("parse created time: %w", err)
	}
	m.UpdatedAt, err = time.Parse(time.RFC3339Nano, u)
	return m, err
}
func (r *Registry) List(ctx context.Context) ([]Model, error) {
	rows, err := r.store.DB.QueryContext(ctx, selectModel+` ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer rows.Close()
	out := []Model{}
	for rows.Next() {
		m, e := scanModel(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Registry) Scan(ctx context.Context) ([]Model, error) {
	entries, err := os.ReadDir(r.root)
	if err != nil {
		return nil, fmt.Errorf("scan models root: %w", err)
	}
	out := []Model{}
	for _, e := range entries {
		if e.Name() == ".quarantine" || !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		p := filepath.Join(r.root, e.Name())
		real, er := r.safeExisting(p)
		if er != nil {
			continue
		}
		var count int
		if er = r.store.DB.QueryRowContext(ctx, `SELECT count(*) FROM models WHERE local_path=?`, real).Scan(&count); er != nil {
			return nil, er
		}
		if count > 0 {
			continue
		}
		m, er := r.RegisterLocal(ctx, e.Name(), real)
		if er != nil {
			return nil, er
		}
		out = append(out, m)
	}
	return out, nil
}

const deletionManifestName = "deletion.json"

type deletionManifest struct {
	ID       string `json:"id"`
	Original string `json:"original"`
}

// ReconcileDeletionQuarantine completes or rolls back model-file deletion after
// an interrupted process. SQLite remains authoritative: an existing model row
// means its staged directory must be restored, while an absent row means the
// committed deletion can be purged. Unknown entries are preserved and fail
// startup rather than risking deletion of operator data.
func (r *Registry) ReconcileDeletionQuarantine(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	rootReal, qroot, entries, err := r.deletionQuarantine()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		id := entry.Name()
		decoded, decodeErr := hex.DecodeString(id)
		if decodeErr != nil || len(id) != 32 || id != strings.ToLower(id) || len(decoded) != 16 || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unknown deletion quarantine entry %q", id)
		}
		stageDir := filepath.Join(qroot, id)
		manifest, readErr := readDeletionManifest(stageDir)
		if readErr != nil {
			return fmt.Errorf("invalid deletion quarantine entry %q: %w", id, readErr)
		}
		if manifest.ID != id {
			return fmt.Errorf("invalid deletion quarantine entry %q: manifest id mismatch", id)
		}
		original, validateErr := validateQuarantinedOriginal(rootReal, manifest.Original)
		if validateErr != nil {
			return fmt.Errorf("invalid deletion quarantine entry %q: %w", id, validateErr)
		}

		var persisted string
		err = r.store.DB.QueryRowContext(ctx, `SELECT COALESCE(local_path,'') FROM models WHERE id=?`, id).Scan(&persisted)
		if errors.Is(err, sql.ErrNoRows) {
			if removeErr := os.RemoveAll(stageDir); removeErr != nil {
				return fmt.Errorf("purge committed model deletion %q: %w", id, removeErr)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("resolve quarantined model %q: %w", id, err)
		}
		if filepath.Clean(persisted) != original {
			return fmt.Errorf("quarantined model %q has inconsistent local path", id)
		}
		staged := filepath.Join(stageDir, "files")
		stagedInfo, statErr := os.Lstat(staged)
		if errors.Is(statErr, os.ErrNotExist) {
			if originalInfo, originalErr := os.Lstat(original); originalErr != nil || !originalInfo.IsDir() || originalInfo.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("quarantined model %q has neither safe staged nor original files", id)
			}
			if removeErr := os.RemoveAll(stageDir); removeErr != nil {
				return fmt.Errorf("clean incomplete deletion stage %q: %w", id, removeErr)
			}
			continue
		}
		if statErr != nil || !stagedInfo.IsDir() || stagedInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe deletion quarantine entry %q", id)
		}
		if _, statErr = os.Lstat(original); !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("cannot restore quarantined model %q: destination exists or cannot be inspected", id)
		}
		if err = os.Rename(staged, original); err != nil {
			return fmt.Errorf("restore quarantined model %q: %w", id, err)
		}
		if err = os.RemoveAll(stageDir); err != nil {
			return fmt.Errorf("remove restored deletion stage %q: %w", id, err)
		}
	}
	if err = os.Remove(qroot); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove empty deletion quarantine: %w", err)
	}
	return nil
}

func (r *Registry) deletionQuarantine() (string, string, []os.DirEntry, error) {
	rootReal, err := filepath.EvalSymlinks(r.root)
	if err != nil {
		return "", "", nil, fmt.Errorf("resolve models root: %w", err)
	}
	qroot := filepath.Join(rootReal, ".quarantine")
	qst, err := os.Lstat(qroot)
	if err != nil {
		return "", "", nil, err
	}
	if !qst.IsDir() || qst.Mode()&os.ModeSymlink != 0 {
		return "", "", nil, errors.New("deletion quarantine is not a private directory")
	}
	if err = os.Chmod(qroot, 0o700); err != nil {
		return "", "", nil, fmt.Errorf("secure deletion quarantine: %w", err)
	}
	entries, err := os.ReadDir(qroot)
	return rootReal, qroot, entries, err
}

func validateQuarantinedOriginal(rootReal, original string) (string, error) {
	original = filepath.Clean(original)
	if !filepath.IsAbs(original) {
		return "", errors.New("original model path is not absolute")
	}
	parentReal, err := filepath.EvalSymlinks(filepath.Dir(original))
	if err != nil {
		return "", fmt.Errorf("resolve original model parent: %w", err)
	}
	original = filepath.Join(parentReal, filepath.Base(original))
	rel, err := filepath.Rel(rootReal, original)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("original model path escapes models root")
	}
	return original, nil
}

func writeDeletionManifest(stageDir string, manifest deletionManifest) error {
	f, err := os.OpenFile(filepath.Join(stageDir, deletionManifestName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err = json.NewEncoder(f).Encode(manifest); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

func readDeletionManifest(stageDir string) (deletionManifest, error) {
	path := filepath.Join(stageDir, deletionManifestName)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 4096 {
		return deletionManifest{}, errors.New("missing or unsafe deletion manifest")
	}
	f, err := os.Open(path)
	if err != nil {
		return deletionManifest{}, err
	}
	defer f.Close()
	var manifest deletionManifest
	decoder := json.NewDecoder(io.LimitReader(f, 4097))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&manifest); err != nil || manifest.ID == "" || manifest.Original == "" {
		return deletionManifest{}, errors.New("invalid deletion manifest")
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return deletionManifest{}, errors.New("invalid trailing deletion manifest data")
	}
	return manifest, nil
}

func (r *Registry) Delete(ctx context.Context, id string, files bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	tx, err := r.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete: %w", err)
	}
	defer tx.Rollback()
	var active, downloading int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_configs WHERE model_id=? AND active=1`, id).Scan(&active); err != nil {
		return fmt.Errorf("check active runtime: %w", err)
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM downloads WHERE (model_id=? OR destination=?) AND state IN ('pending','running')`, id, m.LocalPath).Scan(&downloading); err != nil {
		return fmt.Errorf("check downloads: %w", err)
	}
	if active > 0 || downloading > 0 {
		return errors.New("refusing to delete a running or downloading model")
	}
	var stageDir, staged, original, qroot string
	restoreStage := func(cause error) error {
		if staged == "" {
			return cause
		}
		if restoreErr := os.Rename(staged, original); restoreErr != nil {
			return fmt.Errorf("%w; restore quarantined model files: %v", cause, restoreErr)
		}
		if removeErr := os.RemoveAll(stageDir); removeErr != nil {
			return fmt.Errorf("%w; remove deletion stage: %v", cause, removeErr)
		}
		return cause
	}
	if files {
		if m.LocalPath == "" {
			return errors.New("model has no managed files")
		}
		safe, er := r.safeExisting(m.LocalPath)
		if er != nil {
			return er
		}
		if safe == r.root {
			return errors.New("refusing to delete models root")
		}
		rootReal, er := filepath.EvalSymlinks(r.root)
		if er != nil {
			return fmt.Errorf("resolve models root: %w", er)
		}
		if safe == rootReal {
			return errors.New("refusing to delete models root")
		}
		rel, _ := filepath.Rel(rootReal, safe)
		if rel == "." || strings.HasPrefix(rel, "..") {
			return errors.New("model files are outside configured root")
		}
		st, er := os.Lstat(safe)
		if er != nil {
			return er
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return errors.New("refusing to delete symlink")
		}
		qroot = filepath.Join(rootReal, ".quarantine")
		if er = os.Mkdir(qroot, 0700); er != nil && !errors.Is(er, os.ErrExist) {
			return fmt.Errorf("create deletion quarantine: %w", er)
		}
		qst, er := os.Lstat(qroot)
		if er != nil || !qst.IsDir() || qst.Mode()&os.ModeSymlink != 0 {
			return errors.New("deletion quarantine is not a private directory")
		}
		if er = os.Chmod(qroot, 0700); er != nil {
			return fmt.Errorf("secure deletion quarantine: %w", er)
		}
		stageDir = filepath.Join(qroot, id)
		if er = os.Mkdir(stageDir, 0o700); er != nil {
			return errors.New("model deletion quarantine target already exists")
		}
		original = safe
		if er = writeDeletionManifest(stageDir, deletionManifest{ID: id, Original: original}); er != nil {
			_ = os.RemoveAll(stageDir)
			return fmt.Errorf("write model deletion manifest: %w", er)
		}
		staged = filepath.Join(stageDir, "files")
		if er = os.Rename(original, staged); er != nil {
			_ = os.RemoveAll(stageDir)
			return fmt.Errorf("stage model files for deletion: %w", er)
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM models WHERE id=?`, id)
	if err != nil {
		return restoreStage(fmt.Errorf("delete model: %w", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return restoreStage(sqlite.ErrNotFound)
	}
	if err = tx.Commit(); err != nil {
		return restoreStage(fmt.Errorf("commit model deletion: %w", err))
	}
	if stageDir != "" {
		if err = os.RemoveAll(stageDir); err != nil {
			return fmt.Errorf("model registration deleted; quarantined files remain: %w", err)
		}
		_ = os.Remove(qroot)
	}
	return nil
}
func (r *Registry) safeExisting(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("local model path must be absolute")
	}
	root, err := filepath.EvalSymlinks(r.root)
	if err != nil {
		return "", fmt.Errorf("resolve models root: %w", err)
	}
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve model path: %w", err)
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("model path escapes models root")
	}
	return p, nil
}
func dirSize(root string) (int64, error) {
	var n int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		i, e := d.Info()
		if e != nil {
			return e
		}
		n += i.Size()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("calculate model size: %w", err)
	}
	return n, nil
}
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
