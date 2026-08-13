package sqlite

import (
	"context"
	"encoding/json"
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
	if err = s.PutSettings(ctx, []Setting{{Key: "theme", Value: "dark"}, {Key: "hf_token", Value: "secret", Secret: true}}); err != nil {
		t.Fatal(err)
	}
	settings, err := s.Settings(ctx)
	if err != nil || len(settings) != 2 {
		t.Fatalf("settings=%v err=%v", settings, err)
	}
	for _, v := range settings {
		if v.Secret && v.Value != "" {
			t.Fatal("secret returned")
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
