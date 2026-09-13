package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsRuntimeConfigAndRequestsCRUD(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.PutSettings(ctx, []Setting{{Key: "theme", Value: "dark"}}); err != nil {
		t.Fatal(err)
	}
	settings, err := s.Settings(ctx)
	if err != nil || len(settings) != 1 || settings[0].Key != "theme" || settings[0].Value != "dark" {
		t.Fatalf("settings=%v err=%v", settings, err)
	}
	if err = s.DeleteSetting(ctx, " theme "); err != nil {
		t.Fatalf("delete setting: %v", err)
	}
	settings, err = s.Settings(ctx)
	if err != nil || len(settings) != 0 {
		t.Fatalf("settings after delete=%v err=%v", settings, err)
	}
	if err = s.DeleteSetting(ctx, "theme"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing setting: %v", err)
	}
	for _, sensitive := range []Setting{
		{Key: "hf_token", Value: "secret"},
		{Key: "upstream_api_key", Value: "secret"},
		{Key: "upstream-api-key", Value: "secret"},
		{Key: "upstream_api\u200bkey", Value: "secret"},
		{Key: "oauth.🔐secret", Value: "secret"},
		{Key: "oauth.client-secret", Value: "secret"},
		{Key: "private/key", Value: "secret"},
		{Key: "database.password", Value: "secret"},
	} {
		if err = s.PutSettings(ctx, []Setting{sensitive}); !errors.Is(err, ErrSensitiveSetting) {
			t.Fatalf("sensitive setting %+v: err=%v", sensitive, err)
		}
	}
	if err = s.SaveRuntimeConfig(ctx, RuntimeConfig{ID: "one", Name: "default", Options: json.RawMessage(`{"port":8000}`)}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetActiveRuntime(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordRequest(ctx, APIRequest{RequestID: "req-1", Method: "POST", Path: "/v1/responses", StatusCode: 200, DurationMS: 12}); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordRequest(ctx, APIRequest{RequestID: "req-1", Method: "POST", Path: "/v1/responses", StatusCode: 502, DurationMS: 13}); err != nil {
		t.Fatalf("duplicate client request id should not drop audit event: %v", err)
	}
	recent, err := s.RecentRequests(ctx, 10)
	if err != nil || len(recent) != 2 || recent[0].RequestID != "req-1" || recent[1].RequestID != "req-1" {
		t.Fatalf("recent=%v err=%v", recent, err)
	}
	if recent[0].AuditID == "" || recent[1].AuditID == "" || recent[0].AuditID == recent[1].AuditID {
		t.Fatalf("duplicate correlation IDs need distinct audit identities: %+v", recent)
	}
}

func TestRecordRequestWithLimitBoundsAuditHistory(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	for _, requestID := range []string{"req-1", "req-2", "req-3", "req-4", "req-5"} {
		if err = s.RecordRequestWithLimit(ctx, APIRequest{RequestID: requestID, Method: "POST", Path: "/v1/responses", StatusCode: 200}, 3); err != nil {
			t.Fatalf("record %s: %v", requestID, err)
		}
	}
	recent, err := s.RecentRequests(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 3 || recent[0].RequestID != "req-5" || recent[1].RequestID != "req-4" || recent[2].RequestID != "req-3" {
		t.Fatalf("bounded audit history = %+v", recent)
	}

	if err = s.RecordRequestWithLimit(ctx, APIRequest{RequestID: "disabled", Method: "POST", Path: "/v1/responses", StatusCode: 200}, 0); err != nil {
		t.Fatalf("disable audit recording: %v", err)
	}
	recent, err = s.RecentRequests(ctx, 10)
	if err != nil || len(recent) != 3 || recent[0].RequestID != "req-5" {
		t.Fatalf("disabled recording changed history: recent=%+v err=%v", recent, err)
	}
}

func TestRecordRequestRejectsInvalidMetadataWithoutPersistence(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	invalid := []APIRequest{
		{RequestID: "contains space", Method: "POST", Path: "/v1/responses", StatusCode: 200},
		{RequestID: "req", Method: strings.Repeat("M", 33), Path: "/v1/responses", StatusCode: 200},
		{RequestID: "req", Method: "POST", Path: strings.Repeat("p", 2049), StatusCode: 200},
		{RequestID: "req", Method: "POST", Path: "/v1/responses", Model: string([]byte{0xff}), StatusCode: 200},
		{RequestID: "req", Method: "POST", Path: "/v1/responses", StatusCode: 99},
		{RequestID: "req", Method: "POST", Path: "/v1/responses", StatusCode: 200, DurationMS: -1},
	}
	for i, request := range invalid {
		if err = s.RecordRequest(context.Background(), request); err == nil {
			t.Fatalf("invalid request %d was accepted", i)
		}
	}
	var count int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM api_requests`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid audit writes persisted count=%d err=%v", count, err)
	}
}

func TestSettingsAndAuditReadsRejectCorruptPersistedTimestamps(t *testing.T) {
	t.Run("setting", func(t *testing.T) {
		s, err := Open(filepath.Join(t.TempDir(), "db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err = s.PutSettings(context.Background(), []Setting{{Key: "theme", Value: "dark"}}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.DB.Exec(`UPDATE settings SET updated_at='not-a-time' WHERE key='theme'`); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Settings(context.Background()); err == nil {
			t.Fatal("corrupt setting timestamp was silently accepted")
		}
	})

	t.Run("audit", func(t *testing.T) {
		s, err := Open(filepath.Join(t.TempDir(), "db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err = s.RecordRequest(context.Background(), APIRequest{RequestID: "req", Method: "POST", Path: "/v1/responses", StatusCode: 200}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.DB.Exec(`UPDATE api_requests SET created_at='not-a-time'`); err != nil {
			t.Fatal(err)
		}
		if _, err = s.RecentRequests(context.Background(), 10); err == nil {
			t.Fatal("corrupt audit timestamp was silently accepted")
		}
	})
}

func TestSettingsRejectCorruptPersistedMetadata(t *testing.T) {
	tests := []struct {
		name   string
		column string
		value  any
	}{
		{name: "surrounding whitespace", column: "key", value: " theme"},
		{name: "oversized key", column: "key", value: strings.Repeat("k", 129)},
		{name: "invalid UTF-8 key", column: "key", value: string([]byte{0xff})},
		{name: "control in key", column: "key", value: "theme\nname"},
		{name: "sensitive key", column: "key", value: "api-key"},
		{name: "oversized value", column: "value", value: strings.Repeat("v", 64*1024+1)},
		{name: "invalid UTF-8 value", column: "value", value: string([]byte{0xff})},
		{name: "secret marker", column: "secret", value: 1},
		{name: "invalid secret marker", column: "secret", value: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.PutSettings(context.Background(), []Setting{{Key: "theme", Value: "dark"}}); err != nil {
				t.Fatal(err)
			}
			if _, err = s.DB.Exec(`UPDATE settings SET `+test.column+`=?`, test.value); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Settings(context.Background()); err == nil {
				t.Fatalf("Settings accepted corrupt %s", test.column)
			}
		})
	}
}

func TestRecentRequestsRejectCorruptPersistedMetadata(t *testing.T) {
	tests := []struct {
		name   string
		column string
		value  any
	}{
		{name: "audit id", column: "id", value: "not-an-audit-id"},
		{name: "empty request id", column: "request_id", value: ""},
		{name: "oversized request id", column: "request_id", value: strings.Repeat("r", 129)},
		{name: "non-visible request id", column: "request_id", value: "request id"},
		{name: "empty method", column: "method", value: ""},
		{name: "oversized method", column: "method", value: strings.Repeat("M", 33)},
		{name: "empty path", column: "path", value: ""},
		{name: "oversized path", column: "path", value: "/" + strings.Repeat("p", 2048)},
		{name: "oversized model", column: "model", value: strings.Repeat("m", 513)},
		{name: "invalid UTF-8 model", column: "model", value: string([]byte{0xff})},
		{name: "invalid status", column: "status_code", value: 700},
		{name: "negative duration", column: "duration_ms", value: -1},
		{name: "oversized key id", column: "key_id", value: strings.Repeat("k", 129)},
		{name: "oversized remote address", column: "remote_addr", value: strings.Repeat("a", 257)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.RecordRequest(context.Background(), APIRequest{RequestID: "req", Method: "POST", Path: "/v1/responses", StatusCode: 200}); err != nil {
				t.Fatal(err)
			}
			if test.column == "key_id" {
				if _, err = s.DB.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.DB.Exec(`UPDATE api_requests SET `+test.column+`=?`, test.value); err != nil {
				t.Fatal(err)
			}
			if _, err = s.RecentRequests(context.Background(), 10); err == nil {
				t.Fatalf("RecentRequests accepted corrupt %s", test.column)
			}
		})
	}
}
