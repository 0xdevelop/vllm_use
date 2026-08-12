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
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

var ErrNotFound = errors.New("not found")
