package models

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xdevelop/vllm-use/internal/store"
)

type Model struct {
	ID, Kind, Source, LocalPath string
	CreatedAt                   time.Time
}
type Registry struct {
	store *store.Store
	root  string
}

func New(s *store.Store, root string) *Registry { return &Registry{s, filepath.Clean(root)} }
func id() string                                { b := make([]byte, 16); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func (r *Registry) AddHuggingFace(ctx context.Context, repo string) (Model, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" || strings.HasPrefix(repo, "-") || strings.ContainsAny(repo, "\x00\n\r") {
		return Model{}, errors.New("invalid repository")
	}
	return r.add(ctx, "huggingface", repo, "")
}
func (r *Registry) AddLocal(ctx context.Context, path string) (Model, error) {
	p, err := r.safe(path)
	if err != nil {
		return Model{}, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return Model{}, err
	}
	if !st.IsDir() {
		return Model{}, errors.New("model path must be a directory")
	}
	return r.add(ctx, "local", p, p)
}
func (r *Registry) add(ctx context.Context, k, s, p string) (Model, error) {
	m := Model{ID: id(), Kind: k, Source: s, LocalPath: p, CreatedAt: time.Now().UTC()}
	_, e := r.store.DB.ExecContext(ctx, `INSERT INTO models VALUES(?,?,?,?,?)`, m.ID, k, s, p, m.CreatedAt.Format(time.RFC3339Nano))
	return m, e
}
func (r *Registry) List(ctx context.Context) ([]Model, error) {
	rows, e := r.store.DB.QueryContext(ctx, `SELECT id,kind,source,COALESCE(local_path,''),created_at FROM models ORDER BY created_at`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Model
	for rows.Next() {
		var m Model
		var t string
		if e = rows.Scan(&m.ID, &m.Kind, &m.Source, &m.LocalPath, &t); e != nil {
			return nil, e
		}
		m.CreatedAt, e = time.Parse(time.RFC3339Nano, t)
		if e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (r *Registry) Delete(ctx context.Context, mid string, files bool) error {
	var k, p string
	e := r.store.DB.QueryRowContext(ctx, `SELECT kind,COALESCE(local_path,'') FROM models WHERE id=?`, mid).Scan(&k, &p)
	if errors.Is(e, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if e != nil {
		return e
	}
	if files {
		if k != "local" {
			return errors.New("file deletion only applies to local models")
		}
		safe, e := r.safe(p)
		if e != nil {
			return e
		}
		if safe == r.root {
			return errors.New("refusing to delete models root")
		}
		if e = os.RemoveAll(safe); e != nil {
			return e
		}
	}
	_, e = r.store.DB.ExecContext(ctx, `DELETE FROM models WHERE id=?`, mid)
	return e
}
func (r *Registry) safe(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("local model path must be absolute")
	}
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(r.root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("model path escapes models root")
	}
	return p, nil
}
