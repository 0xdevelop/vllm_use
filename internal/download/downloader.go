package download

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Command interface {
	StdoutPipe() (io.ReadCloser, error)
	StderrPipe() (io.ReadCloser, error)
	Start() error
	Wait() error
}
type Runner interface {
	CommandContext(context.Context, string, ...string) Command
}
type execRunner struct{}
type execCommand struct{ *exec.Cmd }

func (execRunner) CommandContext(c context.Context, n string, a ...string) Command {
	return &execCommand{exec.CommandContext(c, n, a...)}
}

type State string

const (
	Pending   State = "pending"
	Running   State = "running"
	Succeeded State = "succeeded"
	Failed    State = "failed"
	Canceled  State = "canceled"
)

type Job struct {
	ID, Repo, Destination string
	State                 State
	Progress              float64
	Logs                  []string
	Error                 string
	StartedAt, FinishedAt *time.Time
	cancel                context.CancelFunc
}
type Downloader struct {
	mu     sync.RWMutex
	cli    string
	runner Runner
	jobs   map[string]*Job
}

func New(cli string, r Runner) *Downloader {
	if r == nil {
		r = execRunner{}
	}
	return &Downloader{cli: cli, runner: r, jobs: map[string]*Job{}}
}
func (d *Downloader) Download(parent context.Context, id, repo, dest, token string) (*Job, error) {
	if strings.TrimSpace(repo) == "" || strings.HasPrefix(repo, "-") {
		return nil, errors.New("invalid repo")
	}
	d.mu.Lock()
	if j := d.jobs[id]; j != nil && j.State == Running {
		d.mu.Unlock()
		return nil, errors.New("download already running")
	}
	ctx, cancel := context.WithCancel(parent)
	j := &Job{ID: id, Repo: repo, Destination: dest, State: Running, cancel: cancel}
	now := time.Now().UTC()
	j.StartedAt = &now
	d.jobs[id] = j
	d.mu.Unlock()
	args := []string{"download", repo, "--local-dir", dest}
	if token != "" {
		args = append(args, "--token", token)
	}
	cmd := d.runner.CommandContext(ctx, d.cli, args...)
	out, e := cmd.StdoutPipe()
	if e != nil {
		cancel()
		return nil, e
	}
	errout, e := cmd.StderrPipe()
	if e != nil {
		cancel()
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		cancel()
		d.finish(j, Failed, e)
		return j, e
	}
	go d.consume(j, out, token)
	go d.consume(j, errout, token)
	go func() {
		e := cmd.Wait()
		if ctx.Err() != nil {
			d.finish(j, Canceled, ctx.Err())
		} else if e != nil {
			d.finish(j, Failed, e)
		} else {
			d.finish(j, Succeeded, nil)
		}
	}()
	return j, nil
}

var pct = regexp.MustCompile(`([0-9]{1,3}(?:\.[0-9]+)?)%`)

func (d *Downloader) consume(j *Job, r io.Reader, secret string) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := s.Text()
		if secret != "" {
			line = strings.ReplaceAll(line, secret, "[REDACTED]")
		}
		d.mu.Lock()
		j.Logs = append(j.Logs, line)
		if m := pct.FindStringSubmatch(line); len(m) > 0 {
			var v float64
			for _, c := range m[1] {
				if c == '.' {
					continue
				}
				v = v*10 + float64(c-'0')
			}
			if strings.Contains(m[1], ".") {
				v /= 10
			}
			j.Progress = v
		}
		d.mu.Unlock()
	}
}
func (d *Downloader) finish(j *Job, s State, e error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	j.State = s
	now := time.Now().UTC()
	j.FinishedAt = &now
	if e != nil {
		j.Error = e.Error()
	}
	j.cancel = nil
}
func (d *Downloader) Cancel(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	j := d.jobs[id]
	if j == nil {
		return errors.New("job not found")
	}
	if j.cancel != nil {
		j.cancel()
	}
	return nil
}
func (d *Downloader) Status(id string) (Job, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	j, ok := d.jobs[id]
	if !ok {
		return Job{}, false
	}
	cp := *j
	cp.Logs = append([]string(nil), j.Logs...)
	cp.cancel = nil
	return cp, true
}
func (d *Downloader) Retry(ctx context.Context, id, token string) (*Job, error) {
	j, ok := d.Status(id)
	if !ok {
		return nil, errors.New("job not found")
	}
	if j.State == Running {
		return nil, errors.New("download running")
	}
	return d.Download(ctx, id, j.Repo, j.Destination, token)
}
