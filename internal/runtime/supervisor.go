//go:build linux

package runtime

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
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
	ExitCode                      *int
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
	if healthURL != "" {
		u, err := url.Parse(healthURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("invalid health URL")
		}
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
	go s.logs(out)
	go s.logs(errout)
	go s.wait(cmd)
	if healthURL != "" {
		if e = s.poll(ctx, cmd, healthURL); e != nil {
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
		x.active = ""
	}
	if e := x.s.Start(ctx, o, h); e != nil {
		return e
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
