package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/0xdevelop/vllm-use/internal/store"
	"golang.org/x/crypto/scrypt"
)

const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

type Key struct {
	ID, Prefix string
	Enabled    bool
	Scopes     []string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}
type Manager struct{ s *store.Store }

func New(s *store.Store) *Manager { return &Manager{s} }
func random(n int) string {
	b := make([]byte, n)
	r := make([]byte, n)
	_, _ = rand.Read(r)
	for i := range b {
		b[i] = alphabet[int(r[i])%len(alphabet)]
	}
	return string(b)
}
func derive(secret string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(secret), salt, 1<<15, 8, 1, 32)
}
func (m *Manager) Create(ctx context.Context, scopes []string) (Key, string, error) {
	if len(scopes) == 0 {
		return Key{}, "", errors.New("at least one scope required")
	}
	for _, s := range scopes {
		if !validScope(s) {
			return Key{}, "", errors.New("invalid scope")
		}
	}
	secret := "vu_" + random(48)
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return Key{}, "", e
	}
	h, e := derive(secret, salt)
	if e != nil {
		return Key{}, "", e
	}
	now := time.Now().UTC()
	k := Key{ID: random(24), Prefix: secret[:11], Enabled: true, Scopes: scopes, CreatedAt: now}
	_, e = m.s.DB.ExecContext(ctx, `INSERT INTO api_keys VALUES(?,?,?,?,?,?,?,NULL)`, k.ID, k.Prefix, salt, h, 1, strings.Join(scopes, ","), now.Format(time.RFC3339Nano))
	return k, secret, e
}
func validScope(s string) bool {
	switch s {
	case "inference", "mcp.read", "mcp.runtime", "mcp.models", "mcp.admin":
		return true
	}
	return false
}
func (m *Manager) Verify(ctx context.Context, secret, need string) (*Key, error) {
	if len(secret) < 11 {
		return nil, errors.New("invalid API key")
	}
	rows, e := m.s.DB.QueryContext(ctx, `SELECT id,prefix,salt,hash,enabled,scopes,created_at,last_used_at FROM api_keys WHERE prefix=?`, secret[:11])
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var k Key
		var salt, want []byte
		var enabled int
		var scopes, created string
		var last sql.NullString
		if e = rows.Scan(&k.ID, &k.Prefix, &salt, &want, &enabled, &scopes, &created, &last); e != nil {
			return nil, e
		}
		got, e := derive(secret, salt)
		if e != nil {
			return nil, e
		}
		if subtle.ConstantTimeCompare(got, want) == 1 && enabled == 1 {
			k.Enabled = true
			k.Scopes = strings.Split(scopes, ",")
			if need != "" && !has(k.Scopes, need) {
				return nil, errors.New("insufficient scope")
			}
			k.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
			now := time.Now().UTC()
			k.LastUsedAt = &now
			_, _ = m.s.DB.ExecContext(ctx, `UPDATE api_keys SET last_used_at=? WHERE id=?`, now.Format(time.RFC3339Nano), k.ID)
			return &k, nil
		}
	}
	return nil, errors.New("invalid API key")
}
func has(v []string, x string) bool {
	for _, s := range v {
		if s == x || s == "mcp.admin" {
			return true
		}
	}
	return false
}
func Fingerprint(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:6])
}
