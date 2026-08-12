//go:build linux

package runtime

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"os/exec"
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
	Status                        Status
	PID                           int
	StartedAt, ReadyAt, StoppedAt *time.Time
	ExitError                     string
	Logs                          []string
}
type Supervisor struct {
	mu                  sync.RWMutex
	binary              string
	grace, readyTimeout time.Duration
	client              *http.Client
	cmd                 *exec.Cmd
	cancel              context.CancelFunc
	state               State
}

func NewSupervisor(binary string, grace, ready time.Duration) *Supervisor {
	return &Supervisor{binary: binary, grace: grace, readyTimeout: ready, client: &http.Client{Timeout: 2 * time.Second}, state: State{Status: Stopped}}
}
func (s *Supervisor) Start(ctx context.Context, o Options, healthURL string) error {
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
	s.state = State{Status: Starting, PID: cmd.Process.Pid, StartedAt: &now}
	s.mu.Unlock()
	go s.logs(out)
	go s.logs(errout)
	go s.wait(cmd)
	if healthURL != "" {
		if e = s.poll(ctx, healthURL); e != nil {
			_ = s.Stop(context.Background())
			return e
		}
	}
	s.mu.Lock()
	if s.cmd == cmd && s.state.Status == Starting {
		now = time.Now().UTC()
		s.state.Status = Running
		s.state.ReadyAt = &now
	}
	s.mu.Unlock()
	return nil
}
func (s *Supervisor) poll(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, s.readyTimeout)
	defer cancel()
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if res, e := s.client.Do(req); e == nil {
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if res.StatusCode >= 200 && res.StatusCode < 300 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("readiness timeout: " + ctx.Err().Error())
		case <-t.C:
		}
	}
}
func (s *Supervisor) logs(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		s.mu.Lock()
		s.state.Logs = append(s.state.Logs, sc.Text())
		if len(s.state.Logs) > 1000 {
			s.state.Logs = s.state.Logs[len(s.state.Logs)-1000:]
		}
		s.mu.Unlock()
	}
}
func (s *Supervisor) wait(cmd *exec.Cmd) {
	e := cmd.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != cmd {
		return
	}
	now := time.Now().UTC()
	s.state.StoppedAt = &now
	s.state.PID = 0
	if e != nil && s.state.Status != Stopping {
		s.state.Status = Failed
		s.state.ExitError = e.Error()
	} else {
		s.state.Status = Stopped
	}
	s.cmd = nil
	s.cancel = nil
}
func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	cmd := s.cmd
	if cmd == nil {
		s.mu.Unlock()
		return nil
	}
	s.state.Status = Stopping
	pid := cmd.Process.Pid
	s.mu.Unlock()
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	timer := time.NewTimer(s.grace)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			return nil
		case <-time.After(25 * time.Millisecond):
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
	if e := s.Stop(ctx); e != nil {
		return e
	}
	return s.Start(ctx, o, h)
}
func (s *Supervisor) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.state
	v.Logs = append([]string(nil), v.Logs...)
	return v
}

type SwitchService struct {
	mu     sync.Mutex
	s      *Supervisor
	active string
}

func NewSwitchService(s *Supervisor) *SwitchService { return &SwitchService{s: s} }
func (x *SwitchService) Switch(ctx context.Context, id string, o Options, h string) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.active != "" {
		if e := x.s.Stop(ctx); e != nil {
			return e
		}
	}
	if e := x.s.Start(ctx, o, h); e != nil {
		return e
	}
	x.active = id
	return nil
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
