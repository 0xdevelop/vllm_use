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
)

type Status string

const (
	Stopped  Status = "stopped"
	Starting Status = "starting"
	Running  Status = "running"
	Stopping Status = "stopping"
	Failed   Status = "failed"
)

type State struct {
	Status    Status     `json:"status"`
	PID       int        `json:"pid"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	ReadyAt   *time.Time `json:"ready_at,omitempty"`
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
	ExitError string     `json:"exit_error,omitempty"`
	ExitCode  *int       `json:"exit_code,omitempty"`
	Logs      []string   `json:"logs"`
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
	cmd.Env = os.Environ()
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
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		s.mu.Lock()
		if s.cmd != cmd {
			s.mu.Unlock()
			continue
		}
		s.state.Logs = append(s.state.Logs, sc.Text())
		if len(s.state.Logs) > 1000 {
			s.state.Logs = s.state.Logs[len(s.state.Logs)-1000:]
		}
		s.mu.Unlock()
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
	mu       sync.Mutex
	s        *Supervisor
	resolver ModelResolver
	active   string
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
	if x.active != "" {
		if err = x.s.Stop(ctx); err != nil {
			return err
		}
		x.active = ""
	}
	if err = x.s.Start(ctx, o, h); err != nil {
		return err
	}
	x.active = id
	return nil
}
func (x *SwitchService) Active() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	st := x.s.State().Status
	if st != Running && st != Starting {
		x.active = ""
	}
	return x.active
}
func (x *SwitchService) Stop(ctx context.Context) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	e := x.s.Stop(ctx)
	if e == nil {
		x.active = ""
	}
	return e
}
