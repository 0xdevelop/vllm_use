package ability_download

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/0xdevelop/vllm-use/db/sqlite"
	"github.com/0xdevelop/vllm-use/internal/processenv"
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

	maxDownloadLogLineBytes    = 64 << 10
	downloadLogTruncatedSuffix = "… [truncated]"
)

type Job struct {
	ID          string     `json:"id"`
	ModelID     string     `json:"model_id,omitempty"`
	Repo        string     `json:"repository"`
	Revision    string     `json:"revision,omitempty"`
	Destination string     `json:"destination"`
	State       State      `json:"state"`
	Progress    float64    `json:"progress"`
	Logs        []string   `json:"logs"`
	Error       string     `json:"error,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	cancel      context.CancelFunc
	secret      string
}
type Downloader struct {
	mu          sync.RWMutex
	cli         string
	runner      Runner
	jobs        map[string]*Job
	workers     chan struct{}
	maxLogs     int
	store       *sqlite.Store
	root        string
	hfHome      string
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	closed      bool
	terminalErr error
}

func (d *Downloader) SetStore(s *sqlite.Store) error {
	d.mu.Lock()
	d.store = s
	d.mu.Unlock()
	return d.restore()
}
func (d *Downloader) SetRoot(root string) { d.mu.Lock(); d.root = filepath.Clean(root); d.mu.Unlock() }
func (d *Downloader) SetHFHome(home string) {
	d.mu.Lock()
	d.hfHome = strings.TrimSpace(home)
	d.mu.Unlock()
}

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
	ctx, cancel := context.WithCancel(context.Background())
	return &Downloader{cli: cli, runner: r, jobs: map[string]*Job{}, workers: make(chan struct{}, maxWorkers), maxLogs: maxLogs, ctx: ctx, cancel: cancel}
}
func (d *Downloader) Download(parent context.Context, id, repo, dest, token string) (*Job, error) {
	return d.DownloadRequest(parent, Request{ID: id, Repository: repo, Destination: dest, Token: token})
}

// DownloadModel starts a management-plane download from the registered model
// record. Repository, revision and destination are deliberately not accepted
// from the caller: SQLite and the configured models root are authoritative.
func (d *Downloader) DownloadModel(parent context.Context, id, modelID, token string) (*Job, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" || len(modelID) > 128 || strings.ContainsAny(modelID, "\\/\x00\n\r	") {
		return nil, errors.New("invalid model id")
	}
	d.mu.RLock()
	root, st := d.root, d.store
	d.mu.RUnlock()
	if st == nil || root == "" {
		return nil, errors.New("model integration unavailable")
	}
	var kind, repository, revision, status string
	if err := st.DB.QueryRowContext(parent, `SELECT kind,repository,revision,status FROM models WHERE id=?`, modelID).Scan(&kind, &repository, &revision, &status); err != nil {
		return nil, errors.New("registered model not found")
	}
	if kind != "huggingface" {
		return nil, errors.New("only registered Hugging Face models can be downloaded")
	}
	switch status {
	case "registered", "error", "failed", "canceled":
	case "ready":
		return nil, errors.New("model is already ready")
	default:
		return nil, errors.New("model is not available for download")
	}
	return d.DownloadRequest(parent, Request{
		ID:          id,
		ModelID:     modelID,
		Repository:  repository,
		Revision:    revision,
		Destination: filepath.Join(root, modelID),
		Token:       token,
	})
}

type Request struct {
	ID          string `json:"id"`
	ModelID     string `json:"model_id,omitempty"`
	Repository  string `json:"repository"`
	Revision    string `json:"revision,omitempty"`
	Destination string `json:"destination"`
	Token       string `json:"token,omitempty"`
}

func (d *Downloader) DownloadRequest(parent context.Context, request Request) (*Job, error) {
	id, repo, dest, token := request.ID, request.Repository, request.Destination, request.Token
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 128 || strings.ContainsAny(id, "\\/\x00\n\r	") {
		return nil, errors.New("invalid download id")
	}
	if len(token) > 4096 || strings.ContainsAny(token, "\x00\n\r") {
		return nil, errors.New("invalid download token")
	}
	repo = strings.TrimSpace(repo)
	revision := strings.TrimSpace(request.Revision)
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.HasPrefix(repo, "-") || strings.ContainsAny(repo, " \\\x00\n\r\t") || strings.Contains(repo, "..") {
		return nil, errors.New("invalid repo")
	}
	if strings.HasPrefix(revision, "-") || strings.ContainsAny(revision, "\x00\n\r\t ") {
		return nil, errors.New("invalid revision")
	}
	d.mu.RLock()
	root := d.root
	st := d.store
	d.mu.RUnlock()
	modelID := strings.TrimSpace(request.ModelID)
	if modelID != "" {
		if st == nil {
			return nil, errors.New("model integration unavailable")
		}
		var kind, modelRepo, modelRevision string
		if err := st.DB.QueryRowContext(parent, `SELECT kind,repository,revision FROM models WHERE id=?`, modelID).Scan(&kind, &modelRepo, &modelRevision); err != nil {
			return nil, errors.New("registered model not found")
		}
		if kind != "huggingface" || modelRepo != repo {
			return nil, errors.New("download does not match registered model")
		}
		if revision == "" {
			revision = modelRevision
		} else if modelRevision != "" && revision != modelRevision {
			return nil, errors.New("download revision does not match registered model")
		}
	}
	if root != "" {
		if err := validateDownloadDestination(root, dest); err != nil {
			return nil, err
		}
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, errors.New("download service is shutting down")
	}
	if j := d.jobs[id]; j != nil && (j.State == Running || j.State == Pending) {
		d.mu.Unlock()
		return nil, errors.New("download already running")
	}
	for _, existing := range d.jobs {
		if (existing.State == Running || existing.State == Pending) && filepath.Clean(existing.Destination) == filepath.Clean(dest) {
			d.mu.Unlock()
			return nil, errors.New("download destination already in use")
		}
	}
	if token != "" {
		if _, ok := d.runner.CommandContext(parent, d.cli).(interface{ SetEnv([]string) }); !ok {
			d.mu.Unlock()
			return nil, errors.New("download runner cannot securely receive a token")
		}
	}
	// Downloads are service-owned asynchronous work: transport/request
	// cancellation must not terminate them after an accepted response.
	ctx, cancel := context.WithCancel(d.ctx)
	acquiredWorker := false
	select {
	case d.workers <- struct{}{}:
		acquiredWorker = true
	default:
	}
	state := Pending
	if acquiredWorker {
		state = Running
	}
	j := &Job{ID: id, ModelID: modelID, Repo: repo, Revision: revision, Destination: dest, State: state, cancel: cancel, secret: token}
	if acquiredWorker {
		now := time.Now().UTC()
		j.StartedAt = &now
	}
	if err := d.persistAcceptanceLocked(parent, j); err != nil {
		cancel()
		if acquiredWorker {
			<-d.workers
		}
		d.mu.Unlock()
		return nil, fmt.Errorf("persist download acceptance: %w", err)
	}
	d.jobs[id] = j
	d.wg.Add(1)
	d.mu.Unlock()
	if acquiredWorker {
		if err := d.startCommand(j, ctx); err != nil {
			return d.jobCopy(j), err
		}
	} else {
		go d.awaitWorker(j, ctx)
	}
	return d.jobCopy(j), nil
}

// awaitWorker keeps accepted work pending instead of turning temporary worker
// saturation into a failed model download. The job and its model claim are
// already durable before this goroutine begins, so a restart can truthfully
// mark an interrupted queue entry as canceled.
func (d *Downloader) awaitWorker(j *Job, ctx context.Context) {
	select {
	case d.workers <- struct{}{}:
		d.mu.Lock()
		if ctx.Err() == nil && j.State == Pending {
			j.State = Running
			now := time.Now().UTC()
			j.StartedAt = &now
			if err := d.persistLocked(j); err != nil {
				// A queued job must become durably running before the host CLI is
				// launched. Otherwise a crash can restore it as merely pending while
				// an untracked process continues writing model files.
				j.State = Pending
				j.StartedAt = nil
				<-d.workers
				d.mu.Unlock()
				_ = d.finish(j, Failed, fmt.Errorf("persist queued download start: %w", err))
				d.wg.Done()
				return
			}
		}
		d.mu.Unlock()
		if ctx.Err() != nil {
			<-d.workers
			_ = d.finish(j, Canceled, ctx.Err())
			d.wg.Done()
			return
		}
		_ = d.startCommand(j, ctx)
	case <-ctx.Done():
		_ = d.finish(j, Canceled, ctx.Err())
		d.wg.Done()
	}
}

func (d *Downloader) startCommand(j *Job, ctx context.Context) error {
	repo, revision, dest, token := j.Repo, j.Revision, j.Destination, j.secret
	args := []string{"download", repo, "--local-dir", dest}
	if revision != "" {
		args = append(args, "--revision", revision)
	}
	cmd := d.runner.CommandContext(ctx, d.cli, args...)
	if x, ok := cmd.(interface{ SetEnv([]string) }); ok {
		env := processenv.WithoutManagerCredentials(os.Environ())
		if token != "" {
			env = setEnvironment(env, "HF_TOKEN", token)
		}
		if d.hfHome != "" {
			env = setEnvironment(env, "HF_HOME", d.hfHome)
		}
		x.SetEnv(env)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		d.failStart(j, err)
		return err
	}
	errout, err := cmd.StderrPipe()
	if err != nil {
		d.failStart(j, err)
		return err
	}
	if err = cmd.Start(); err != nil {
		d.failStart(j, err)
		return err
	}
	pipeErrors := make(chan error, 2)
	go func() { pipeErrors <- d.consume(j, out, token) }()
	go func() { pipeErrors <- d.consume(j, errout, token) }()
	go func() {
		defer d.wg.Done()
		var pipeErr error
		for i := 0; i < 2; i++ {
			if consumeErr := <-pipeErrors; consumeErr != nil && pipeErr == nil {
				pipeErr = consumeErr
			}
		}
		// StdoutPipe/StderrPipe require reads to complete before Wait; calling
		// Wait first can close a still-buffered pipe and turn a successful CLI
		// run into a spurious "file already closed" failure.
		waitErr := cmd.Wait()
		if waitErr == nil {
			waitErr = pipeErr
		}
		<-d.workers
		if ctx.Err() != nil {
			_ = d.finish(j, Canceled, ctx.Err())
		} else if waitErr != nil {
			_ = d.finish(j, Failed, waitErr)
		} else {
			_ = d.finish(j, Succeeded, nil)
		}
	}()
	return nil
}

func (d *Downloader) failStart(j *Job, err error) {
	<-d.workers
	_ = d.finish(j, Failed, err)
	d.wg.Done()
}

func (d *Downloader) jobCopy(j *Job) *Job {
	d.mu.RLock()
	defer d.mu.RUnlock()
	response := *j
	response.Logs = append([]string(nil), j.Logs...)
	response.cancel = nil
	response.secret = ""
	return &response
}

var pct = regexp.MustCompile(`([0-9]{1,3}(?:\.[0-9]+)?)%`)

func (d *Downloader) consume(j *Job, r io.Reader, secret string) error {
	reader := bufio.NewReaderSize(r, 64<<10)
	captureLimit := maxDownloadLogLineBytes + len(secret)
	line := make([]byte, 0, min(captureLimit, 64<<10))
	tail := make([]byte, 0, 256)
	truncated := false
	haveLine := false
	for {
		part, more, err := reader.ReadLine()
		if err == nil || len(part) > 0 {
			haveLine = true
		}
		if len(part) > 0 {
			remaining := captureLimit - len(line)
			if remaining > 0 {
				take := min(remaining, len(part))
				line = append(line, part[:take]...)
				truncated = truncated || take < len(part)
			} else {
				truncated = true
			}
			tail = append(tail, part...)
			if len(tail) > 256 {
				tail = append(tail[:0], tail[len(tail)-256:]...)
			}
		}
		if !more && (haveLine || truncated) {
			d.appendLogLine(j, string(line), string(tail), secret, truncated)
			line = line[:0]
			tail = tail[:0]
			truncated = false
			haveLine = false
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return errors.New("read download output: " + err.Error())
		}
	}
}

func (d *Downloader) appendLogLine(j *Job, line, progressTail, secret string, truncated bool) {
	if secret != "" {
		line = strings.ReplaceAll(line, secret, "[REDACTED]")
	}
	line = strings.ToValidUTF8(line, "�")
	if len(line) > maxDownloadLogLineBytes {
		line = line[:maxDownloadLogLineBytes]
		for len(line) > 0 && !utf8.ValidString(line) {
			line = line[:len(line)-1]
		}
		truncated = true
	}
	if truncated {
		line += downloadLogTruncatedSuffix
	}
	d.mu.Lock()
	j.Logs = append(j.Logs, line)
	if len(j.Logs) > d.maxLogs {
		j.Logs = append([]string(nil), j.Logs[len(j.Logs)-d.maxLogs:]...)
	}
	matches := pct.FindAllStringSubmatch(progressTail, -1)
	if len(matches) > 0 {
		if v, err := strconv.ParseFloat(matches[len(matches)-1][1], 64); err == nil && v >= 0 && v <= 100 {
			j.Progress = v
		}
	}
	d.mu.Unlock()
	d.persist(j)
}
func (d *Downloader) finish(j *Job, s State, e error) error {
	localPath := ""
	var size int64
	if s == Succeeded && e == nil {
		localPath, size, e = d.completedDownload(j.Destination)
		if e != nil {
			s = Failed
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	j.State = s
	now := time.Now().UTC()
	j.FinishedAt = &now
	if e != nil {
		j.Error = e.Error()
		if j.secret != "" {
			j.Error = strings.ReplaceAll(j.Error, j.secret, "[REDACTED]")
		}
	}
	j.cancel = nil
	j.secret = ""
	if err := d.persistTerminalLocked(j, localPath, size); err != nil {
		wrapped := fmt.Errorf("persist terminal download state: %w", err)
		j.State = Failed
		j.Error = wrapped.Error()
		d.terminalErr = errors.Join(d.terminalErr, wrapped)
		return wrapped
	}
	return nil
}

func setEnvironment(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, prefix) {
			out = append(out, item)
		}
	}
	return append(out, prefix+value)
}

func validateDownloadDestination(root, destination string) error {
	if !filepath.IsAbs(destination) {
		return errors.New("download destination must be absolute")
	}
	root = filepath.Clean(root)
	clean := filepath.Clean(destination)
	rel, err := filepath.Rel(root, clean)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("download destination must be inside models root")
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return errors.New("resolve models root: " + err.Error())
	}

	// Hugging Face reopens --local-dir by pathname. Reject an existing link (or
	// any linked parent) before launching it so a pre-planted model path cannot
	// redirect writes outside the managed root. A final completion check still
	// catches replacement while the external process is running.
	ancestor := clean
	for {
		info, inspectErr := os.Lstat(ancestor)
		if inspectErr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("download destination path must not contain symlinks")
			}
			if ancestor == clean && !info.IsDir() {
				return errors.New("download destination must be a directory")
			}
			break
		}
		if !errors.Is(inspectErr, os.ErrNotExist) {
			return errors.New("inspect download destination: " + inspectErr.Error())
		}
		next := filepath.Dir(ancestor)
		if next == ancestor {
			return errors.New("download destination has no existing parent")
		}
		ancestor = next
	}
	for path := ancestor; ; path = filepath.Dir(path) {
		info, inspectErr := os.Lstat(path)
		if inspectErr != nil {
			return errors.New("inspect download destination parent: " + inspectErr.Error())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("download destination path must not contain symlinks")
		}
		if path == root {
			break
		}
		next := filepath.Dir(path)
		if next == path {
			return errors.New("download destination parent escapes models root")
		}
	}
	real, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return errors.New("resolve download destination parent: " + err.Error())
	}
	realRel, err := filepath.Rel(rootReal, real)
	if err != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
		return errors.New("download destination parent escapes models root")
	}
	return nil
}

func (d *Downloader) completedDownload(destination string) (string, int64, error) {
	info, err := os.Lstat(destination)
	if err != nil {
		return "", 0, errors.New("download destination is unavailable: " + err.Error())
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", 0, errors.New("download destination must not be a symlink")
	}
	if !info.IsDir() {
		return "", 0, errors.New("download destination must be a directory")
	}
	real, err := filepath.EvalSymlinks(destination)
	if err != nil {
		return "", 0, errors.New("resolve download destination: " + err.Error())
	}
	d.mu.RLock()
	root := d.root
	d.mu.RUnlock()
	if root != "" {
		rootReal, resolveErr := filepath.EvalSymlinks(root)
		if resolveErr != nil {
			return "", 0, errors.New("resolve models root: " + resolveErr.Error())
		}
		rel, relErr := filepath.Rel(rootReal, real)
		if relErr != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", 0, errors.New("download destination escapes models root")
		}
	}
	size, err := directorySize(real)
	if err != nil {
		return "", 0, errors.New("inspect download destination: " + err.Error())
	}
	return real, size, nil
}

func directorySize(root string) (int64, error) {
	var size int64
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	})
	return size, err
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
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (d *Downloader) restore() error {
	d.mu.RLock()
	store := d.store
	d.mu.RUnlock()
	if store == nil {
		return nil
	}
	rows, err := store.DB.Query(`SELECT id,COALESCE(model_id,''),repository,revision,destination,state,progress,error,logs,started_at,finished_at FROM downloads ORDER BY id`)
	if err != nil {
		return fmt.Errorf("read persisted downloads: %w", err)
	}
	type restoredJob struct {
		job         Job
		interrupted bool
	}
	var restored []restoredJob
	for rows.Next() {
		var j Job
		var state, logs string
		var started, finished *string
		if err = rows.Scan(&j.ID, &j.ModelID, &j.Repo, &j.Revision, &j.Destination, &state, &j.Progress, &j.Error, &logs, &started, &finished); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan persisted download: %w", err)
		}
		j.State = State(state)
		interrupted := j.State == Running || j.State == Pending
		if err = json.Unmarshal([]byte(logs), &j.Logs); err != nil {
			_ = rows.Close()
			return fmt.Errorf("decode persisted download %q logs: %w", j.ID, err)
		}
		if started != nil {
			v, parseErr := time.Parse(time.RFC3339Nano, *started)
			if parseErr != nil {
				_ = rows.Close()
				return fmt.Errorf("parse persisted download %q start time: %w", j.ID, parseErr)
			}
			j.StartedAt = &v
		}
		if finished != nil {
			v, parseErr := time.Parse(time.RFC3339Nano, *finished)
			if parseErr != nil {
				_ = rows.Close()
				return fmt.Errorf("parse persisted download %q finish time: %w", j.ID, parseErr)
			}
			j.FinishedAt = &v
		}
		if interrupted {
			j.State, j.Error = Canceled, "interrupted by service restart"
			now := time.Now().UTC()
			j.FinishedAt = &now
		}
		restored = append(restored, restoredJob{job: j, interrupted: interrupted})
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("read persisted downloads: %w", err)
	}
	// Close the SELECT cursor before terminal-state transactions. Restore must
	// work with a one-connection SQLite pool instead of waiting for a connection
	// still owned by its own read cursor.
	if err = rows.Close(); err != nil {
		return fmt.Errorf("close persisted download cursor: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range restored {
		j := &restored[i].job
		if restored[i].interrupted {
			if err = d.persistTerminalLocked(j, "", 0); err != nil {
				wrapped := fmt.Errorf("persist interrupted download state: %w", err)
				d.terminalErr = errors.Join(d.terminalErr, wrapped)
				return wrapped
			}
		}
		d.jobs[j.ID] = j
	}
	return nil
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
	if j.State == Running || j.State == Pending {
		return nil, errors.New("download running")
	}
	if j.State != Failed && j.State != Canceled {
		return nil, errors.New("only failed or canceled downloads can be retried")
	}
	if j.ModelID == "" {
		return nil, errors.New("download is not linked to a registered model")
	}
	// Resolve source, revision, destination and current eligibility from the
	// registered model again. Persisted job fields are historical audit data and
	// must not become authority for a new host-side download attempt.
	return d.DownloadModel(ctx, id, j.ModelID, token)
}

// Shutdown stops accepting new work, cancels every active download, and waits
// for accepted jobs to reach a persisted terminal state.
func (d *Downloader) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	if !d.closed {
		d.closed = true
		d.cancel()
	}
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		d.mu.RLock()
		err := d.terminalErr
		d.mu.RUnlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Downloader) persist(j *Job) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_ = d.persistLocked(j)
}

// persistAcceptanceLocked makes the accepted job and its linked model state
// durable in one transaction before any host process is started. An
// asynchronous download must never exist only in memory: otherwise a restart
// loses the job while the Hugging Face process may still be writing files.
func (d *Downloader) persistAcceptanceLocked(ctx context.Context, j *Job) error {
	if d.store == nil {
		return nil
	}
	tx, err := d.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	logs, err := json.Marshal(j.Logs)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var started any
	if j.StartedAt != nil {
		started = j.StartedAt.Format(time.RFC3339Nano)
	}
	if _, err = tx.ExecContext(ctx, downloadUpsertSQL, j.ID, nullValue(j.ModelID), j.Repo, j.Revision, j.Destination, string(j.State), j.Progress, j.Error, string(logs), started, nil, now, now); err != nil {
		return err
	}
	if j.ModelID != "" {
		// Claim only a currently retryable model state in the same transaction as
		// the job upsert. This closes the gap between DownloadModel's read and host
		// process launch, so a stale request cannot move a ready/downloading model
		// backwards and overwrite its files.
		result, updateErr := tx.ExecContext(ctx, `UPDATE models SET status='downloading',updated_at=? WHERE id=? AND status IN ('registered','error','failed','canceled')`, now, j.ModelID)
		if updateErr != nil {
			return updateErr
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		if rows != 1 {
			return errors.New("registered model is not available for download")
		}
	}
	return tx.Commit()
}

const downloadUpsertSQL = `INSERT INTO downloads(id,model_id,repository,revision,destination,state,progress,error,logs,started_at,finished_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET model_id=excluded.model_id,repository=excluded.repository,revision=excluded.revision,destination=excluded.destination,state=excluded.state,progress=excluded.progress,error=excluded.error,logs=excluded.logs,started_at=excluded.started_at,finished_at=excluded.finished_at,updated_at=excluded.updated_at`

func (d *Downloader) persistLocked(j *Job) error {
	if d.store == nil {
		return nil
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
	_, err := d.store.DB.ExecContext(context.Background(), downloadUpsertSQL, j.ID, nullValue(j.ModelID), j.Repo, j.Revision, j.Destination, string(j.State), j.Progress, j.Error, string(logs), started, finished, now, now)
	return err
}

// persistTerminalLocked publishes a linked download's terminal job and model
// state in one SQLite transaction. Keeping these writes atomic prevents a
// succeeded job from becoming durable while its model remains downloading (or
// the inverse) after a database error or process restart.
func (d *Downloader) persistTerminalLocked(j *Job, localPath string, size int64) error {
	if d.store == nil {
		return nil
	}
	tx, err := d.store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	logs, err := json.Marshal(j.Logs)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var started, finished any
	if j.StartedAt != nil {
		started = j.StartedAt.Format(time.RFC3339Nano)
	}
	if j.FinishedAt != nil {
		finished = j.FinishedAt.Format(time.RFC3339Nano)
	}
	if _, err = tx.ExecContext(context.Background(), downloadUpsertSQL, j.ID, nullValue(j.ModelID), j.Repo, j.Revision, j.Destination, string(j.State), j.Progress, j.Error, string(logs), started, finished, now, now); err != nil {
		return err
	}
	if j.ModelID != "" {
		status := "error"
		switch j.State {
		case Running, Pending:
			status = "downloading"
		case Succeeded:
			if localPath != "" {
				status = "ready"
			}
		case Canceled:
			status = "canceled"
		}
		var result sql.Result
		if status == "ready" && localPath != "" {
			result, err = tx.ExecContext(context.Background(), `UPDATE models SET status=?,local_path=?,size_bytes=?,updated_at=? WHERE id=?`, status, localPath, size, now, j.ModelID)
		} else {
			result, err = tx.ExecContext(context.Background(), `UPDATE models SET status=?,updated_at=? WHERE id=?`, status, now, j.ModelID)
		}
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			return errors.New("registered model disappeared before terminal download persistence")
		}
	}
	return tx.Commit()
}

func nullValue(value string) any {
	if value == "" {
		return nil
	}
	return value
}
