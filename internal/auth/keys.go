package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/0xdevelop/vllm-use/internal/store"
	"golang.org/x/crypto/scrypt"
)

const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

var ErrInvalidKey = errors.New("invalid API key")
var ErrInsufficientScope = errors.New("insufficient scope")

type Key struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Enabled    bool       `json:"enabled"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}
type Manager struct{ s *store.Store }

func New(s *store.Store) *Manager { return &Manager{s} }
func random(n int) (string, error) {
	r := make([]byte, n)
	if _, err := rand.Read(r); err != nil {
		return "", fmt.Errorf("random bytes: %w", err)
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[int(r[i])%len(alphabet)]
	}
	return string(b), nil
}
func derive(secret string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(secret), salt, 1<<15, 8, 1, 32)
}
func (m *Manager) Create(ctx context.Context, scopes []string) (Key, string, error) {
	return m.CreateNamed(ctx, "", scopes)
}
func (m *Manager) CreateNamed(ctx context.Context, name string, scopes []string) (Key, string, error) {
	scopes = normalizeScopes(scopes)
	if len(scopes) == 0 {
		return Key{}, "", errors.New("at least one scope required")
	}
	for _, s := range scopes {
		if !validScope(s) {
			return Key{}, "", fmt.Errorf("invalid scope %q", s)
		}
	}
	name = strings.TrimSpace(name)
	if len(name) > 100 {
		return Key{}, "", errors.New("key name too long")
	}
	for tries := 0; tries < 5; tries++ {
		tail, e := random(48)
		if e != nil {
			return Key{}, "", e
		}
		secret := "vu_" + tail
		salt := make([]byte, 16)
		if _, e = rand.Read(salt); e != nil {
			return Key{}, "", fmt.Errorf("generate salt: %w", e)
		}
		h, e := derive(secret, salt)
		if e != nil {
			return Key{}, "", e
		}
		kid, e := random(24)
		if e != nil {
			return Key{}, "", e
		}
		now := time.Now().UTC()
		k := Key{ID: kid, Name: name, Prefix: secret[:11], Enabled: true, Scopes: scopes, CreatedAt: now}
		_, e = m.s.DB.ExecContext(ctx, `INSERT INTO api_keys(id,prefix,salt,hash,enabled,scopes,created_at,last_used_at,name) VALUES(?,?,?,?,?,?,?,NULL,?)`, k.ID, k.Prefix, salt, h, 1, strings.Join(scopes, ","), now.Format(time.RFC3339Nano), name)
		if e == nil {
			return k, secret, nil
		}
		if !strings.Contains(strings.ToLower(e.Error()), "unique") {
			return Key{}, "", fmt.Errorf("create API key: %w", e)
		}
	}
	return Key{}, "", errors.New("could not allocate unique key prefix")
}
func validScope(s string) bool {
	switch s {
	case "inference", "admin.read", "admin.write", "mcp.read", "mcp.runtime", "mcp.models", "mcp.admin":
		return true
	}
	return false
}
func normalizeScopes(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
func (m *Manager) Verify(ctx context.Context, secret, need string) (*Key, error) {
	if len(secret) < 11 {
		return nil, ErrInvalidKey
	}
	rows, e := m.s.DB.QueryContext(ctx, `SELECT id,name,prefix,salt,hash,enabled,scopes,created_at,last_used_at FROM api_keys WHERE prefix=?`, secret[:11])
	if e != nil {
		return nil, fmt.Errorf("lookup API key: %w", e)
	}
	defer rows.Close()
	for rows.Next() {
		var k Key
		var salt, want []byte
		var enabled int
		var scopes, created string
		var last sql.NullString
		if e = rows.Scan(&k.ID, &k.Name, &k.Prefix, &salt, &want, &enabled, &scopes, &created, &last); e != nil {
			return nil, e
		}
		got, e := derive(secret, salt)
		if e != nil {
			return nil, e
		}
		if subtle.ConstantTimeCompare(got, want) == 1 {
			if enabled != 1 {
				return nil, ErrInvalidKey
			}
			k.Enabled = true
			k.Scopes = strings.Split(scopes, ",")
			if need != "" && !has(k.Scopes, need) {
				return nil, ErrInsufficientScope
			}
			k.CreatedAt, e = time.Parse(time.RFC3339Nano, created)
			if e != nil {
				return nil, e
			}
			if last.Valid {
				t, _ := time.Parse(time.RFC3339Nano, last.String)
				k.LastUsedAt = &t
			}
			now := time.Now().UTC()
			k.LastUsedAt = &now
			if _, e = m.s.DB.ExecContext(ctx, `UPDATE api_keys SET last_used_at=? WHERE id=?`, now.Format(time.RFC3339Nano), k.ID); e != nil {
				return nil, fmt.Errorf("update key usage: %w", e)
			}
			return &k, nil
		}
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return nil, ErrInvalidKey
}
func has(v []string, x string) bool {
	for _, s := range v {
		if s == x || s == "mcp.admin" || (s == "admin.write" && x == "admin.read") {
			return true
		}
	}
	return false
}
func (m *Manager) List(ctx context.Context) ([]Key, error) {
	rows, e := m.s.DB.QueryContext(ctx, `SELECT id,name,prefix,enabled,scopes,created_at,last_used_at FROM api_keys ORDER BY created_at DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		var k Key
		var en int
		var scopes, c string
		var l sql.NullString
		if e = rows.Scan(&k.ID, &k.Name, &k.Prefix, &en, &scopes, &c, &l); e != nil {
			return nil, e
		}
		k.Enabled = en == 1
		k.Scopes = strings.Split(scopes, ",")
		k.CreatedAt, _ = time.Parse(time.RFC3339Nano, c)
		if l.Valid {
			t, _ := time.Parse(time.RFC3339Nano, l.String)
			k.LastUsedAt = &t
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (m *Manager) SetEnabled(ctx context.Context, id string, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	res, e := m.s.DB.ExecContext(ctx, `UPDATE api_keys SET enabled=? WHERE id=?`, v, id)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}
func (m *Manager) Delete(ctx context.Context, id string) error {
	res, e := m.s.DB.ExecContext(ctx, `DELETE FROM api_keys WHERE id=?`, id)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}
func Fingerprint(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:6])
}
