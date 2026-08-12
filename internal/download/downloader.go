package download

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/0xdevelop/vllm-use/internal/store"
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
	cmd := exec.CommandContext(c, n, a...)
	configureProcessGroup(cmd)
	return &execCommand{cmd}
}
func (c *execCommand) SetEnv(env []string) { c.Env = env }

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
	mu      sync.RWMutex
	cli     string
	runner  Runner
	jobs    map[string]*Job
	workers chan struct{}
	maxLogs int
	store   *store.Store
	root    string
}

func (d *Downloader) SetStore(s *store.Store) { d.mu.Lock(); d.store = s; d.mu.Unlock() }
func (d *Downloader) SetRoot(root string)     { d.mu.Lock(); d.root = filepath.Clean(root); d.mu.Unlock() }

func New(cli string, r Runner) *Downloader {
	if r == nil {
		r = execRunner{}
	}
	return NewWithOptions(cli, r, 2, 1000)
}
func NewWithOptions(cli string, r Runner, maxWorkers, maxLogs int) *Downloader {
	if r == nil {
		r = execRunner{}
	}
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	if maxLogs < 1 {
		maxLogs = 1
	}
	return &Downloader{cli: cli, runner: r, jobs: map[string]*Job{}, workers: make(chan struct{}, maxWorkers), maxLogs: maxLogs}
}
func (d *Downloader) Download(parent context.Context, id, repo, dest, token string) (*Job, error) {
	repo = strings.TrimSpace(repo)
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.HasPrefix(repo, "-") || strings.ContainsAny(repo, " \\\x00\n\r\t") || strings.Contains(repo, "..") {
		return nil, errors.New("invalid repo")
	}
	d.mu.RLock()
	root := d.root
	d.mu.RUnlock()
	if root != "" {
		if !filepath.IsAbs(dest) {
			return nil, errors.New("download destination must be absolute")
		}
		clean := filepath.Clean(dest)
		rel, e := filepath.Rel(root, clean)
		if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, errors.New("download destination must be inside models root")
		}
		if real, e := filepath.EvalSymlinks(filepath.Dir(clean)); e == nil {
			rootReal, re := filepath.EvalSymlinks(root)
			rr, _ := filepath.Rel(rootReal, real)
			if re != nil || rr == ".." || strings.HasPrefix(rr, ".."+string(filepath.Separator)) {
				return nil, errors.New("download destination parent escapes models root")
			}
		}
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
	d.persistLocked(j)
	d.mu.Unlock()
	select {
	case d.workers <- struct{}{}:
	case <-parent.Done():
		d.finish(j, Canceled, parent.Err())
		return j, parent.Err()
	default:
		d.finish(j, Failed, errors.New("maximum concurrent downloads reached"))
		return j, errors.New("maximum concurrent downloads reached")
	}
	args := []string{"download", repo, "--local-dir", dest}
	cmd := d.runner.CommandContext(ctx, d.cli, args...)
	if x, ok := cmd.(interface{ SetEnv([]string) }); ok {
		env := os.Environ()
		if token != "" {
			env = append(env, "HF_TOKEN="+token)
		}
		env = append(env, "HF_HOME="+dest)
		x.SetEnv(env)
	} else if token != "" { // Compatibility for injected runners that cannot set an environment.
		args = append(args, "--token", token)
		cmd = d.runner.CommandContext(ctx, d.cli, args...)
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		cancel()
		<-d.workers
		d.finish(j, Failed, e)
		return j, e
	}
	errout, e := cmd.StderrPipe()
	if e != nil {
		cancel()
		<-d.workers
		d.finish(j, Failed, e)
		return j, e
	}
	if e = cmd.Start(); e != nil {
		cancel()
		<-d.workers
		d.finish(j, Failed, e)
		return j, e
	}
	pipeErrors := make(chan error, 2)
	go func() { pipeErrors <- d.consume(j, out, token) }()
	go func() { pipeErrors <- d.consume(j, errout, token) }()
	go func() {
		e := cmd.Wait()
		for i := 0; i < 2; i++ {
			if pe := <-pipeErrors; pe != nil && e == nil {
				e = pe
			}
		}
		<-d.workers
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

func (d *Downloader) consume(j *Job, r io.Reader, secret string) error {
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := s.Text()
		if secret != "" {
			line = strings.ReplaceAll(line, secret, "[REDACTED]")
		}
		d.mu.Lock()
		j.Logs = append(j.Logs, line)
		if len(j.Logs) > d.maxLogs {
			j.Logs = append([]string(nil), j.Logs[len(j.Logs)-d.maxLogs:]...)
		}
		if m := pct.FindStringSubmatch(line); len(m) > 0 {
			if v, e := strconv.ParseFloat(m[1], 64); e == nil && v >= 0 && v <= 100 {
				j.Progress = v
			}
		}
		d.mu.Unlock()
		d.persist(j)
	}
	if err := s.Err(); err != nil {
		return errors.New("read download output: " + err.Error())
	}
	return nil
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
	d.persistLocked(j)
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
func (d *Downloader) List() []Job {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]Job, 0, len(d.jobs))
	for _, j := range d.jobs {
		cp := *j
		cp.Logs = append([]string(nil), j.Logs...)
		cp.cancel = nil
		out = append(out, cp)
	}
	return out
}
func (d *Downloader) Logs(id string) ([]string, error) {
	j, ok := d.Status(id)
	if !ok {
		return nil, errors.New("job not found")
	}
	return j.Logs, nil
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
func (d *Downloader) persist(j *Job) { d.mu.RLock(); defer d.mu.RUnlock(); d.persistLocked(j) }
func (d *Downloader) persistLocked(j *Job) {
	if d.store == nil {
		return
	}
	logs, _ := json.Marshal(j.Logs)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var started, finished any
	if j.StartedAt != nil {
		started = j.StartedAt.Format(time.RFC3339Nano)
	}
	if j.FinishedAt != nil {
		finished = j.FinishedAt.Format(time.RFC3339Nano)
	}
	_, _ = d.store.DB.ExecContext(context.Background(), `INSERT INTO downloads(id,repository,destination,state,progress,error,logs,started_at,finished_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,progress=excluded.progress,error=excluded.error,logs=excluded.logs,started_at=excluded.started_at,finished_at=excluded.finished_at,updated_at=excluded.updated_at`, j.ID, j.Repo, j.Destination, string(j.State), j.Progress, j.Error, string(logs), started, finished, now, now)
}
