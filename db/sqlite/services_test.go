package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
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
