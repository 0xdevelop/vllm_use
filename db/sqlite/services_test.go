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
	recent, err := s.RecentRequests(ctx, 10)
	if err != nil || len(recent) != 1 || recent[0].RequestID != "req-1" {
		t.Fatalf("recent=%v err=%v", recent, err)
	}
}
