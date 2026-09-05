package sqlite

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

type Setting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

var ErrSensitiveSetting = errors.New("sensitive settings must be supplied through environment variables or CLI flags")

var sensitiveSettingFragments = []string{
	"token", "secret", "password", "credential", "apikey", "authorization", "privatekey",
}

func normalizedSettingKey(key string) string {
	var normalized strings.Builder
	normalized.Grow(len(key))
	for _, r := range key {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			normalized.WriteRune(unicode.ToLower(r))
		}
	}
	return normalized.String()
}

func validateSetting(v *Setting) error {
	v.Key = strings.TrimSpace(v.Key)
	if v.Key == "" {
		return errors.New("setting key required")
	}
	if len(v.Key) > 128 {
		return errors.New("setting key exceeds 128 bytes")
	}
	if len(v.Value) > 64*1024 {
		return errors.New("setting value exceeds 64 KiB")
	}
	// Compare a separator-free form so cosmetic spelling cannot turn an
	// API-key/credential field into a persistable non-sensitive setting.
	key := normalizedSettingKey(v.Key)
	for _, fragment := range sensitiveSettingFragments {
		if strings.Contains(key, fragment) {
			return ErrSensitiveSetting
		}
	}
	return nil
}

func (s *Store) Settings(ctx context.Context) ([]Setting, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT key,value,updated_at FROM settings ORDER BY key`)
	if e != nil {
		return nil, fmt.Errorf("list settings: %w", e)
	}
	defer rows.Close()
	out := []Setting{}
	for rows.Next() {
		var v Setting
		var ts string
		if e = rows.Scan(&v.Key, &v.Value, &ts); e != nil {
			return nil, e
		}
		v.UpdatedAt, _ = time.Parse(time.RFC3339Nano, ts)
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
	for i := range values {
		v := &values[i]
		if e = validateSetting(v); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO settings(key,value,secret,updated_at) VALUES(?,?,0,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,secret=0,updated_at=excluded.updated_at`, v.Key, v.Value, time.Now().UTC().Format(time.RFC3339Nano))
		if e != nil {
			return fmt.Errorf("update setting %q: %w", v.Key, e)
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("setting key required")
	}
	if len(key) > 128 {
		return errors.New("setting key exceeds 128 bytes")
	}
	result, err := s.DB.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, key)
	if err != nil {
		return fmt.Errorf("delete setting %q: %w", key, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete setting %q: %w", key, err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
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
	AuditID    string    `json:"audit_id"`
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

const DefaultMaxAuditRecords = 10_000

func (s *Store) RecordRequest(ctx context.Context, v APIRequest) error {
	return s.RecordRequestWithLimit(ctx, v, DefaultMaxAuditRecords)
}

// RecordRequestWithLimit appends one audit event and atomically removes the
// oldest events beyond maxRecords. A zero limit disables new audit writes but
// deliberately leaves existing operator history intact.
func (s *Store) RecordRequestWithLimit(ctx context.Context, v APIRequest, maxRecords int) error {
	if maxRecords < 0 {
		return errors.New("maximum audit records must not be negative")
	}
	if maxRecords == 0 {
		return nil
	}
	idBytes := make([]byte, 16)
	if _, e := rand.Read(idBytes); e != nil {
		return fmt.Errorf("generate request audit id: %w", e)
	}
	id := hex.EncodeToString(idBytes)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin request audit: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO api_requests(id,request_id,method,path,model,status_code,duration_ms,key_id,remote_addr,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, v.RequestID, v.Method, v.Path, v.Model, v.StatusCode, v.DurationMS, null(v.KeyID), v.RemoteAddr, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("record request audit: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM api_requests WHERE id IN (SELECT id FROM api_requests ORDER BY created_at DESC,id DESC LIMIT -1 OFFSET ?)`, maxRecords); err != nil {
		return fmt.Errorf("prune request audits: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit request audit: %w", err)
	}
	return nil
}
func (s *Store) RecentRequests(ctx context.Context, limit int) ([]APIRequest, error) {
	if limit < 1 || limit > 500 {
		limit = 50
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT id,request_id,method,path,model,status_code,duration_ms,COALESCE(key_id,''),remote_addr,created_at FROM api_requests ORDER BY created_at DESC,id DESC LIMIT ?`, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []APIRequest{}
	for rows.Next() {
		var v APIRequest
		var ts string
		if e = rows.Scan(&v.AuditID, &v.RequestID, &v.Method, &v.Path, &v.Model, &v.StatusCode, &v.DurationMS, &v.KeyID, &v.RemoteAddr, &ts); e != nil {
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
