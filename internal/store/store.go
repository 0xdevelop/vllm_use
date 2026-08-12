package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// A small pool permits an authentication lookup to update last-used metadata
	// while its read cursor is still open. WAL and busy_timeout serialize writers.
	db.SetMaxOpenConns(4)
	s := &Store{DB: db}
	if err = s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

var migrations = []string{
	`CREATE TABLE models (id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK(kind IN ('huggingface','local')), source TEXT NOT NULL, local_path TEXT, created_at TEXT NOT NULL); CREATE TABLE api_keys (id TEXT PRIMARY KEY, prefix TEXT NOT NULL, salt BLOB NOT NULL, hash BLOB NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, scopes TEXT NOT NULL, created_at TEXT NOT NULL, last_used_at TEXT);`,
	`CREATE INDEX api_keys_prefix_idx ON api_keys(prefix);`,
	`ALTER TABLE models ADD COLUMN name TEXT NOT NULL DEFAULT ''; ALTER TABLE models ADD COLUMN repository TEXT NOT NULL DEFAULT ''; ALTER TABLE models ADD COLUMN revision TEXT NOT NULL DEFAULT ''; ALTER TABLE models ADD COLUMN size_bytes INTEGER NOT NULL DEFAULT 0; ALTER TABLE models ADD COLUMN status TEXT NOT NULL DEFAULT 'registered'; ALTER TABLE models ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''; UPDATE models SET name=CASE WHEN instr(source,'/')>0 THEN substr(source,instr(source,'/')+1) ELSE source END, repository=CASE WHEN kind='huggingface' THEN source ELSE '' END, updated_at=created_at;`,
	`ALTER TABLE api_keys ADD COLUMN name TEXT NOT NULL DEFAULT ''; CREATE UNIQUE INDEX api_keys_prefix_unique_idx ON api_keys(prefix); CREATE TABLE runtime_configs (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, model_id TEXT, options_json TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, FOREIGN KEY(model_id) REFERENCES models(id) ON DELETE SET NULL); CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, secret INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL); CREATE TABLE downloads (id TEXT PRIMARY KEY, model_id TEXT, repository TEXT NOT NULL, revision TEXT NOT NULL DEFAULT '', destination TEXT NOT NULL, state TEXT NOT NULL, progress REAL NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', logs TEXT NOT NULL DEFAULT '[]', started_at TEXT, finished_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, FOREIGN KEY(model_id) REFERENCES models(id) ON DELETE SET NULL); CREATE TABLE api_requests (id TEXT PRIMARY KEY, request_id TEXT NOT NULL UNIQUE, method TEXT NOT NULL, path TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', status_code INTEGER NOT NULL, duration_ms INTEGER NOT NULL, key_id TEXT, remote_addr TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, FOREIGN KEY(key_id) REFERENCES api_keys(id) ON DELETE SET NULL); CREATE INDEX downloads_created_idx ON downloads(created_at DESC); CREATE INDEX api_requests_created_idx ON api_requests(created_at DESC);`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	var v int
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, migrations[i]); err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations VALUES(?,?)`, i+1, time.Now().UTC().Format(time.RFC3339Nano))
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", i+1, err)
		}
	}
	return nil
}

var ErrNotFound = errors.New("not found")
