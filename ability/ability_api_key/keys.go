package ability_api_key

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
	"unicode"
	"unicode/utf8"

	"github.com/0xdevelop/vllm-use/db/sqlite"
	"golang.org/x/crypto/scrypt"
)

const (
	alphabet            = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	secretPrefix        = "vu_"
	secretRandomLength  = 48
	displayedPrefixSize = 11
	saltSize            = 16
	hashSize            = 32
)

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
type Manager struct{ s *sqlite.Store }

func New(s *sqlite.Store) *Manager { return &Manager{s} }
func random(n int) (string, error) {
	b := make([]byte, n)
	randomBytes := make([]byte, 32)
	// Discard the high tail that cannot be divided evenly by len(alphabet),
	// avoiding modulo bias in credentials and public identifiers.
	limit := byte(256 - (256 % len(alphabet)))
	for written := 0; written < n; {
		if _, err := rand.Read(randomBytes); err != nil {
			return "", fmt.Errorf("random bytes: %w", err)
		}
		for _, value := range randomBytes {
			if value >= limit {
				continue
			}
			b[written] = alphabet[int(value)%len(alphabet)]
			written++
			if written == n {
				break
			}
		}
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
	if err := validateKeyName(name); err != nil {
		return Key{}, "", err
	}
	for tries := 0; tries < 5; tries++ {
		tail, e := random(secretRandomLength)
		if e != nil {
			return Key{}, "", e
		}
		secret := secretPrefix + tail
		salt := make([]byte, saltSize)
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
		k := Key{ID: kid, Name: name, Prefix: secret[:displayedPrefixSize], Enabled: true, Scopes: scopes, CreatedAt: now}
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

func validateKeyName(name string) error {
	if len(name) > 100 || !utf8.ValidString(name) {
		return errors.New("key name must be valid UTF-8 up to 100 bytes")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errors.New("key name must not contain control characters")
		}
	}
	return nil
}

func decodeScopes(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	seen := make(map[string]struct{}, len(parts))
	for _, scope := range parts {
		if scope == "" || !validScope(scope) {
			return nil, errors.New("invalid persisted API key scope")
		}
		if _, exists := seen[scope]; exists {
			return nil, errors.New("duplicate persisted API key scope")
		}
		seen[scope] = struct{}{}
	}
	return parts, nil
}

func decodeEnabled(value int) (bool, error) {
	switch value {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, errors.New("invalid persisted API key enabled flag")
	}
}

func validDisplayedPrefix(prefix string) bool {
	if len(prefix) != displayedPrefixSize || !strings.HasPrefix(prefix, secretPrefix) {
		return false
	}
	for i := len(secretPrefix); i < len(prefix); i++ {
		if !strings.ContainsRune(alphabet, rune(prefix[i])) {
			return false
		}
	}
	return true
}
func (m *Manager) Verify(ctx context.Context, secret, need string) (*Key, error) {
	if !validSecret(secret) {
		return nil, ErrInvalidKey
	}
	// Prefix is unique. QueryRow closes its read cursor as soon as Scan returns,
	// before the last-used write below. Keeping a Rows cursor open across that
	// write makes authentication depend on a spare database connection and can
	// deadlock a saturated pool when every verifier waits for its own update.
	var k Key
	var salt, want []byte
	var enabled int
	var scopes, created string
	var last sql.NullString
	e := m.s.DB.QueryRowContext(ctx, `SELECT id,name,prefix,salt,hash,enabled,scopes,created_at,last_used_at FROM api_keys WHERE prefix=?`, secret[:displayedPrefixSize]).Scan(&k.ID, &k.Name, &k.Prefix, &salt, &want, &enabled, &scopes, &created, &last)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrInvalidKey
	}
	if e != nil {
		return nil, fmt.Errorf("lookup API key: %w", e)
	}
	if err := validateKeyName(k.Name); err != nil {
		return nil, fmt.Errorf("decode API key %q name: %w", k.ID, err)
	}
	if !validDisplayedPrefix(k.Prefix) || len(salt) != saltSize || len(want) != hashSize {
		return nil, fmt.Errorf("decode API key %q credential metadata: invalid persisted credential material", k.ID)
	}
	k.Enabled, e = decodeEnabled(enabled)
	if e != nil {
		return nil, fmt.Errorf("decode API key %q: %w", k.ID, e)
	}
	k.Scopes, e = decodeScopes(scopes)
	if e != nil {
		return nil, fmt.Errorf("decode API key %q: %w", k.ID, e)
	}
	k.CreatedAt, e = time.Parse(time.RFC3339Nano, created)
	if e != nil {
		return nil, fmt.Errorf("parse API key %q creation time: %w", k.ID, e)
	}
	if last.Valid {
		t, parseErr := time.Parse(time.RFC3339Nano, last.String)
		if parseErr != nil {
			return nil, fmt.Errorf("parse API key %q last-used time: %w", k.ID, parseErr)
		}
		k.LastUsedAt = &t
	}
	// Decode every persisted authorization field before doing the KDF or
	// publishing an authenticated principal. Corrupt SQLite rows must fail
	// closed rather than partially authenticating with a valid scope fragment.
	got, e := derive(secret, salt)
	if e != nil {
		return nil, e
	}
	if subtle.ConstantTimeCompare(got, want) != 1 || !k.Enabled {
		return nil, ErrInvalidKey
	}
	if need != "" && !has(k.Scopes, need) {
		return nil, ErrInsufficientScope
	}
	now := time.Now().UTC()
	k.LastUsedAt = &now
	if _, e = m.s.DB.ExecContext(ctx, `UPDATE api_keys SET last_used_at=? WHERE id=?`, now.Format(time.RFC3339Nano), k.ID); e != nil {
		return nil, fmt.Errorf("update key usage: %w", e)
	}
	return &k, nil
}
func validSecret(secret string) bool {
	if len(secret) != len(secretPrefix)+secretRandomLength || !strings.HasPrefix(secret, secretPrefix) {
		return false
	}
	for i := len(secretPrefix); i < len(secret); i++ {
		if !strings.ContainsRune(alphabet, rune(secret[i])) {
			return false
		}
	}
	return true
}
func has(scopes []string, required string) bool {
	for _, scope := range scopes {
		if scope == required || (scope == "admin.write" && required == "admin.read") {
			return true
		}
		// MCP administration is an umbrella only inside the MCP namespace. It
		// must never authenticate management HTTP or inference requests.
		if scope == "mcp.admin" && strings.HasPrefix(required, "mcp.") {
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
		if err := validateKeyName(k.Name); err != nil {
			return nil, fmt.Errorf("decode API key %q name: %w", k.ID, err)
		}
		if !validDisplayedPrefix(k.Prefix) {
			return nil, fmt.Errorf("decode API key %q prefix: invalid persisted prefix", k.ID)
		}
		k.Enabled, e = decodeEnabled(en)
		if e != nil {
			return nil, fmt.Errorf("decode API key %q: %w", k.ID, e)
		}
		k.Scopes, e = decodeScopes(scopes)
		if e != nil {
			return nil, fmt.Errorf("decode API key %q: %w", k.ID, e)
		}
		k.CreatedAt, e = time.Parse(time.RFC3339Nano, c)
		if e != nil {
			return nil, fmt.Errorf("parse API key %q creation time: %w", k.ID, e)
		}
		if l.Valid {
			t, parseErr := time.Parse(time.RFC3339Nano, l.String)
			if parseErr != nil {
				return nil, fmt.Errorf("parse API key %q last-used time: %w", k.ID, parseErr)
			}
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
		return sqlite.ErrNotFound
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
		return sqlite.ErrNotFound
	}
	return nil
}
func Fingerprint(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:6])
}
