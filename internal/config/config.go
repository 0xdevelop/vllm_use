package config

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	Listen, DataDir, Database, ModelsDir, VLLMBinary, HFCLI, Upstream string
	AdminToken                                                        string
	ReadinessTimeout, ShutdownGrace                                   time.Duration
}

func Default() Config {
	d, _ := os.UserConfigDir()
	d = filepath.Join(d, "vllm-use")
	return Config{Listen: "127.0.0.1:8080", DataDir: d, Database: filepath.Join(d, "vllm-use.db"), ModelsDir: filepath.Join(d, "models"), VLLMBinary: "vllm", HFCLI: "hf", Upstream: "http://127.0.0.1:8000", ReadinessTimeout: 2 * time.Minute, ShutdownGrace: 10 * time.Second}
}

func (c Config) Validate() error {
	if c.DataDir == "" || c.Database == "" || c.ModelsDir == "" {
		return errors.New("data, database, and models paths are required")
	}
	if !filepath.IsAbs(c.DataDir) || !filepath.IsAbs(c.Database) || !filepath.IsAbs(c.ModelsDir) {
		return errors.New("data paths must be absolute")
	}
	if c.VLLMBinary == "" || c.HFCLI == "" {
		return errors.New("vllm and hf executables are required")
	}
	if c.ReadinessTimeout <= 0 || c.ShutdownGrace <= 0 {
		return errors.New("timeouts must be positive")
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return err
	}
	if net.ParseIP(host) == nil && host != "localhost" {
		return errors.New("listen host must be an IP address or localhost")
	}
	return nil
}

func (c Config) IsLoopback() bool {
	h, _, _ := net.SplitHostPort(c.Listen)
	return h == "localhost" || (net.ParseIP(h) != nil && net.ParseIP(h).IsLoopback())
}

func (c Config) Prepare() error {
	if err := c.Validate(); err != nil {
		return err
	}
	for _, d := range []string{c.DataDir, c.ModelsDir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			return err
		}
		if err := os.Chmod(d, 0700); err != nil {
			return err
		}
	}
	return nil
}
