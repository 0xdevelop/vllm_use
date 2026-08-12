package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestValidationAndLoopback(t *testing.T) {
	d := t.TempDir()
	c := Config{Listen: "127.0.0.1:8080", DataDir: d, Database: filepath.Join(d, "db"), ModelsDir: filepath.Join(d, "models"), VLLMBinary: "vllm", HFCLI: "hf", ReadinessTimeout: time.Second, ShutdownGrace: time.Second}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	if !c.IsLoopback() {
		t.Fatal("loopback not detected")
	}
	c.Listen = "0.0.0.0:8080"
	if c.IsLoopback() {
		t.Fatal("wildcard treated as loopback")
	}
	c.ModelsDir = "relative"
	if e := c.Validate(); e == nil {
		t.Fatal("relative path accepted")
	}
}
