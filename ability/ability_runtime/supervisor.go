//go:build linux

package ability_runtime

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/0xdevelop/vllm-use/internal/processenv"
)

type Status string

const (
	Stopped  Status = "stopped"
	Starting Status = "starting"
	Running  Status = "running"
	Stopping Status = "stopping"
	Failed   Status = "failed"

	maxRuntimeLogLineBytes    = 64 << 10
	runtimeLogTruncatedSuffix = "… [truncated]"
)

type State struct {
	Status        Status     `json:"status"`
	PID           int        `json:"pid"`
	ActiveModelID string     `json:"active_model_id,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	ReadyAt       *time.Time `json:"ready_at,omitempty"`
	StoppedAt     *time.Time `json:"stopped_at,omitempty"`
	ExitError     string     `json:"exit_error,omitempty"`
	ExitCode      *int       `json:"exit_code,omitempty"`
	Logs          []string   `json:"logs"`
}
type Supervisor struct {
	opMu                sync.Mutex
	mu                  sync.RWMutex
	binary              string
	grace, readyTimeout time.Duration
	client              *http.Client
	cmd                 *exec.Cmd
	cancel              context.CancelFunc
	state               State
	healthInterval      time.Duration
	done                chan struct{}
}

func NewSupervisor(binary string, grace, ready time.Duration) *Supervisor {
	return &Supervisor{binary: binary, grace: grace, readyTimeout: ready, healthInterval: 200 * time.Millisecond, client: &http.Client{Timeout: 2 * time.Second}, state: State{Status: Stopped}}
}
func (s *Supervisor) SetHealthInterval(d time.Duration) {
	if d > 0 {
		s.mu.Lock()
		s.healthInterval = d
		s.mu.Unlock()
	}
}
func (s *Supervisor) Start(ctx context.Context, o Options, healthURL string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	return s.start(ctx, o, healthURL)
}
func (s *Supervisor) start(ctx context.Context, o Options, healthURL string) error {
	if healthURL != "" {
		return errors.New("health_url is not accepted; readiness is derived from runtime options")
	}
	args, e := BuildArgs(o)
	if e != nil {
		return e
	}
	s.mu.Lock()
	if s.state.Status == Starting || s.state.Status == Running || s.state.Status == Stopping {
		s.mu.Unlock()
		return errors.New("runtime already active")
	}
	runctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(runctx, s.binary, args...)
	cmd.Env = processenv.WithoutManagerCredentials(os.Environ())
	if len(o.GPUDevices) > 0 {
		devices := make([]string, len(o.GPUDevices))
		for i, device := range o.GPUDevices {
			devices[i] = strconv.Itoa(device)
		}
		cmd.Env = setEnv(cmd.Env, "CUDA_VISIBLE_DEVICES", strings.Join(devices, ","))
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, e := cmd.StdoutPipe()
	if e != nil {
		s.mu.Unlock()
		cancel()
		return e
	}
	errout, e := cmd.StderrPipe()
	if e != nil {
		s.mu.Unlock()
		cancel()
		return e
	}
	if e = cmd.Start(); e != nil {
		s.mu.Unlock()
		cancel()
		return e
	}
	now := time.Now().UTC()
	s.cmd = cmd
	s.cancel = cancel
	s.done = make(chan struct{})
	s.state = State{Status: Starting, PID: cmd.Process.Pid, StartedAt: &now}
	s.mu.Unlock()
	go s.logs(cmd, out)
	go s.logs(cmd, errout)
	go s.wait(cmd)
	host := o.Host
	if host == "" {
		host = "127.0.0.1"
	}
	healthURL = (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(o.Port)), Path: "/health"}).String()
	if e = s.poll(ctx, cmd, healthURL); e != nil {
		_ = s.stop(context.Background())
		return e
	}
	s.mu.Lock()
	if s.cmd == cmd && s.state.Status == Starting {
		now = time.Now().UTC()
		s.state.Status = Running
		s.state.ReadyAt = &now
	}
	s.mu.Unlock()
	if s.State().Status != Running {
		return errors.New("runtime exited before readiness")
	}
	return nil
}
func (s *Supervisor) poll(ctx context.Context, cmd *exec.Cmd, url string) error {
	ctx, cancel := context.WithTimeout(ctx, s.readyTimeout)
	defer cancel()
	s.mu.RLock()
	interval := s.healthInterval
	done := s.done
	s.mu.RUnlock()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return errors.New("invalid health URL")
		}
		if res, e := s.client.Do(req); e == nil {
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if res.StatusCode >= 200 && res.StatusCode < 300 {
				return nil
			}
		}
		select {
		case <-done:
			return errors.New("runtime exited before readiness")
		case <-ctx.Done():
			return errors.New("readiness timeout: " + ctx.Err().Error())
		case <-t.C:
		}
	}
}
func (s *Supervisor) logs(cmd *exec.Cmd, r io.Reader) {
	reader := bufio.NewReaderSize(r, 64<<10)
	line := make([]byte, 0, maxRuntimeLogLineBytes)
	truncated := false
	haveLine := false
	for {
		part, more, err := reader.ReadLine()
		if err == nil || len(part) > 0 {
			haveLine = true
		}
		if len(part) > 0 {
			remaining := maxRuntimeLogLineBytes - len(line)
			if remaining > 0 {
				take := min(remaining, len(part))
				line = append(line, part[:take]...)
				truncated = truncated || take < len(part)
			} else {
				truncated = true
			}
		}
		if !more && haveLine {
			s.appendLog(cmd, string(line), truncated)
			line = line[:0]
			truncated = false
			haveLine = false
		}
		if err != nil {
			return
		}
	}
}

func (s *Supervisor) appendLog(cmd *exec.Cmd, line string, truncated bool) {
	line = strings.ToValidUTF8(line, "�")
	if len(line) > maxRuntimeLogLineBytes {
		line = line[:maxRuntimeLogLineBytes]
		for len(line) > 0 && !utf8.ValidString(line) {
			line = line[:len(line)-1]
		}
		truncated = true
	}
	if truncated {
		line += runtimeLogTruncatedSuffix
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != cmd {
		return
	}
	s.state.Logs = append(s.state.Logs, line)
	if len(s.state.Logs) > 1000 {
		s.state.Logs = append([]string(nil), s.state.Logs[len(s.state.Logs)-1000:]...)
	}
}
func (s *Supervisor) wait(cmd *exec.Cmd) {
	e := cmd.Wait()
	// The leader may exit while descendants keep its pipes and process group alive.
	// Always tear down that original group before publishing completion.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != cmd {
		return
	}
	now := time.Now().UTC()
	s.state.StoppedAt = &now
	s.state.PID = 0
	code := cmd.ProcessState.ExitCode()
	s.state.ExitCode = &code
	if e != nil && s.state.Status != Stopping {
		s.state.Status = Failed
		s.state.ExitError = e.Error()
	} else {
		s.state.Status = Stopped
	}
	s.cmd = nil
	s.cancel = nil
	if s.done != nil {
		close(s.done)
		s.done = nil
	}
}
func (s *Supervisor) Stop(ctx context.Context) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	return s.stop(ctx)
}
func (s *Supervisor) stop(ctx context.Context) error {
	s.mu.Lock()
	cmd := s.cmd
	if cmd == nil {
		s.mu.Unlock()
		return nil
	}
	s.state.Status = Stopping
	pid := cmd.Process.Pid
	done := s.done
	s.mu.Unlock()
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	timer := time.NewTimer(s.grace)
	defer timer.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			if done != nil {
				<-done
			}
			return ctx.Err()
		case <-timer.C:
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			if done != nil {
				<-done
			}
			return nil
		case <-tick.C:
			s.mu.RLock()
			done := s.cmd != cmd
			s.mu.RUnlock()
			if done {
				return nil
			}
		}
	}
}
func (s *Supervisor) Restart(ctx context.Context, o Options, h string) error {
	// Validate the replacement before stopping a healthy runtime. Restart is a
	// destructive operation once stop begins, so malformed options must fail
	// without causing an avoidable outage.
	if h != "" {
		return errors.New("health_url is not accepted; readiness is derived from runtime options")
	}
	if _, err := BuildArgs(o); err != nil {
		return err
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if e := s.stop(ctx); e != nil {
		return e
	}
	return s.start(ctx, o, h)
}
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, prefix) {
			out = append(out, item)
		}
	}
	return append(out, prefix+value)
}

func (s *Supervisor) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.state
	v.Logs = append([]string(nil), v.Logs...)
	return v
}

type SwitchService struct {
	mu        sync.Mutex
	s         *Supervisor
	resolver  ModelResolver
	active    string
	modelPath string
}

func NewSwitchService(s *Supervisor) *SwitchService { return &SwitchService{s: s} }

type ModelTarget struct {
	ID        string
	LocalPath string
	Status    string
}

type ModelResolver func(context.Context, string) (ModelTarget, error)

func (x *SwitchService) SetModelResolver(resolve ModelResolver) {
	x.mu.Lock()
	x.resolver = resolve
	x.mu.Unlock()
}

func (x *SwitchService) Switch(ctx context.Context, id string, o Options, h string) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.resolver == nil {
		return errors.New("runtime model resolver unavailable")
	}
	target, err := x.resolver(ctx, id)
	if err != nil {
		return err
	}
	if target.ID != id {
		return errors.New("runtime model resolver returned a mismatched model")
	}
	if target.Status != "ready" || target.LocalPath == "" || !filepath.IsAbs(target.LocalPath) {
		return errors.New("model is not ready for runtime")
	}
	canonical := filepath.Clean(target.LocalPath)
	if o.Model != "" && filepath.Clean(o.Model) != canonical {
		return errors.New("runtime options model does not match model_id")
	}
	o.Model = canonical
	status := x.s.State().Status
	if status == Starting || status == Running || status == Stopping {
		if err = x.s.Stop(ctx); err != nil {
			return err
		}
	}
	x.active = ""
	x.modelPath = ""
	if err = x.s.Start(ctx, o, h); err != nil {
		return err
	}
	x.active = id
	x.modelPath = canonical
	return nil
}

// Start launches options that are not associated with a registry model. A
// failed duplicate start preserves any existing association; a successful
// direct start intentionally clears it.
func (x *SwitchService) Start(ctx context.Context, o Options, h string) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if err := x.s.Start(ctx, o, h); err != nil {
		return err
	}
	x.active = ""
	x.modelPath = filepath.Clean(o.Model)
	return nil
}

// Restart replaces the current runtime without claiming that the replacement
// still serves the previously registered model. Supervisor.Restart validates
// before stopping, so an invalid preflight keeps both the process and its
// association intact.
func (x *SwitchService) Restart(ctx context.Context, o Options, h string) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	err := x.s.Restart(ctx, o, h)
	status := x.s.State().Status
	if err == nil {
		x.active = ""
		x.modelPath = filepath.Clean(o.Model)
	} else if status != Starting && status != Running {
		x.active = ""
		x.modelPath = ""
	}
	return err
}

func (x *SwitchService) Active() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	st := x.s.State().Status
	if st != Running && st != Starting {
		x.active = ""
		x.modelPath = ""
	}
	return x.active
}
func (x *SwitchService) Stop(ctx context.Context) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	e := x.s.Stop(ctx)
	if e == nil {
		x.active = ""
		x.modelPath = ""
	}
	return e
}

// GuardModelDeletion serializes a model deletion with every runtime lifecycle
// operation. Checking only Active() before deleting has a race: a switch can
// resolve the model after the check and start it while its files are being
// quarantined. The path check also protects a model launched through the
// lower-level direct start/restart methods, which intentionally have no model
// registry association.
func (x *SwitchService) GuardModelDeletion(id, localPath string, remove func() error) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	state := x.s.State().Status
	active := state == Starting || state == Running || state == Stopping
	samePath := localPath != "" && x.modelPath != "" && filepath.Clean(localPath) == filepath.Clean(x.modelPath)
	if active && (x.active == id || samePath) {
		return errors.New("refusing to delete the running model")
	}
	return remove()
}
