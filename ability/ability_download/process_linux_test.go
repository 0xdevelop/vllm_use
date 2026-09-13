//go:build linux

package ability_download

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestConfigureProcessGroupKillsLeaderWhenManagerDies(t *testing.T) {
	cmd := exec.Command("true")
	configureProcessGroup(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("download command must run in its own process group")
	}
	if cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("download parent-death signal = %v, want SIGKILL", cmd.SysProcAttr.Pdeathsig)
	}
}

func TestConfiguredProcessLeaderDiesWithParent(t *testing.T) {
	if pidFile := os.Getenv("VLLM_USE_PDEATH_HELPER"); pidFile != "" {
		cmd := exec.CommandContext(context.Background(), "sleep", "30")
		configureProcessGroup(cmd)
		if err := cmd.Start(); err != nil {
			_ = os.WriteFile(pidFile, []byte("start: "+err.Error()), 0o600)
			os.Exit(2)
		}
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}

	pidFile := filepath.Join(t.TempDir(), "child.pid")
	helper := exec.Command(os.Args[0], "-test.run=^TestConfiguredProcessLeaderDiesWithParent$")
	helper.Env = append(os.Environ(), "VLLM_USE_PDEATH_HELPER="+pidFile)
	if output, err := helper.CombinedOutput(); err != nil {
		detail, _ := os.ReadFile(pidFile)
		t.Fatalf("parent helper failed: %v: %s %s", err, output, detail)
	}
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		stat, readErr := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		if errors.Is(readErr, os.ErrNotExist) {
			return
		}
		if readErr == nil {
			fields := strings.Fields(string(stat))
			if len(fields) >= 3 && fields[2] == "Z" {
				return
			}
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("configured child %d survived its parent", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
