package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Setting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value,omitempty"`
	Secret    bool      `json:"secret"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Store) Settings(ctx context.Context) ([]Setting, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT key,value,secret,updated_at FROM settings ORDER BY key`)
	if e != nil {
		return nil, fmt.Errorf("list settings: %w", e)
	}
	defer rows.Close()
	out := []Setting{}
	for rows.Next() {
		var v Setting
		var sec int
		var ts string
		if e = rows.Scan(&v.Key, &v.Value, &sec, &ts); e != nil {
			return nil, e
		}
		v.Secret = sec == 1
		v.UpdatedAt, _ = time.Parse(time.RFC3339Nano, ts)
		if v.Secret {
			v.Value = ""
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) PutSettings(ctx context.Context, values []Setting) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, v := range values {
		if v.Key == "" {
			return errors.New("setting key required")
		}
		if v.Secret && v.Value == "" {
			continue
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO settings(key,value,secret,updated_at) VALUES(?,?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,secret=excluded.secret,updated_at=excluded.updated_at`, v.Key, v.Value, boolInt(v.Secret), time.Now().UTC().Format(time.RFC3339Nano))
		if e != nil {
			return fmt.Errorf("update setting %q: %w", v.Key, e)
		}
	}
	return tx.Commit()
}

type RuntimeConfig struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	ModelID              string          `json:"model_id,omitempty"`
	Options              json.RawMessage `json:"options"`
	Active               bool            `json:"active"`
	CreatedAt, UpdatedAt time.Time
}

func (s *Store) SaveRuntimeConfig(ctx context.Context, v RuntimeConfig) error {
	if !json.Valid(v.Options) {
		return errors.New("runtime options must be valid JSON")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, e := s.DB.ExecContext(ctx, `INSERT INTO runtime_configs(id,name,model_id,options_json,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,model_id=excluded.model_id,options_json=excluded.options_json,updated_at=excluded.updated_at`, v.ID, v.Name, null(v.ModelID), string(v.Options), boolInt(v.Active), now, now)
	return e
}
func (s *Store) SetActiveRuntime(ctx context.Context, id string) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `UPDATE runtime_configs SET active=0`); e != nil {
		return e
	}
	if id != "" {
		res, er := tx.ExecContext(ctx, `UPDATE runtime_configs SET active=1,updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), id)
		if er != nil {
			return er
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
	}
	return tx.Commit()
}

type APIRequest struct {
	RequestID  string    `json:"request_id"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Model      string    `json:"model,omitempty"`
	KeyID      string    `json:"key_id,omitempty"`
	RemoteAddr string    `json:"remote_addr"`
	StatusCode int       `json:"status_code"`
	DurationMS int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Store) RecordRequest(ctx context.Context, v APIRequest) error {
	_, e := s.DB.ExecContext(ctx, `INSERT INTO api_requests(id,request_id,method,path,model,status_code,duration_ms,key_id,remote_addr,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, v.RequestID, v.RequestID, v.Method, v.Path, v.Model, v.StatusCode, v.DurationMS, null(v.KeyID), v.RemoteAddr, time.Now().UTC().Format(time.RFC3339Nano))
	return e
}
func (s *Store) RecentRequests(ctx context.Context, limit int) ([]APIRequest, error) {
	if limit < 1 || limit > 500 {
		limit = 50
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT request_id,method,path,model,status_code,duration_ms,COALESCE(key_id,''),remote_addr,created_at FROM api_requests ORDER BY created_at DESC LIMIT ?`, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []APIRequest{}
	for rows.Next() {
		var v APIRequest
		var ts string
		if e = rows.Scan(&v.RequestID, &v.Method, &v.Path, &v.Model, &v.StatusCode, &v.DurationMS, &v.KeyID, &v.RemoteAddr, &ts); e != nil {
			return nil, e
		}
		v.CreatedAt, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, v)
	}
	return out, rows.Err()
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func null(v string) any {
	if v == "" {
		return nil
	}
	return v
}
