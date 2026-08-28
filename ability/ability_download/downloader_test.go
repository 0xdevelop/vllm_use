package ability_download

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xdevelop/vllm-use/db/sqlite"
)

type fakeRunner struct {
	cmd  *fakeCmd
	name string
	args []string
}

func TestDownloadDestinationStaysInsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	d := New("hf", &fakeRunner{cmd: &fakeCmd{}})
	d.SetRoot(root)
	for _, tc := range []struct{ id, destination string }{
		{"", filepath.Join(root, "model")},
		{"slash/id", filepath.Join(root, "model")},
		{"outside", filepath.Join(outside, "model")},
		{"symlink", filepath.Join(root, "escape", "new", "model")},
		{"root", root},
	} {
		if _, err := d.Download(context.Background(), tc.id, "org/model", tc.destination, ""); err == nil {
			t.Fatalf("accepted id=%q destination=%q", tc.id, tc.destination)
		}
	}
}

func (f *fakeRunner) CommandContext(_ context.Context, n string, a ...string) Command {
	f.name = n
	f.args = append([]string(nil), a...)
	return f.cmd
}

type fakeCmd struct {
	out, errout string
	wait        error
	env         []string
}

func (f *fakeCmd) SetEnv(env []string) { f.env = append([]string(nil), env...) }

func (f *fakeCmd) StdoutPipe() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.out)), nil
}
func (f *fakeCmd) StderrPipe() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.errout)), nil
}
func (f *fakeCmd) Start() error { return nil }
func (f *fakeCmd) Wait() error  { return f.wait }
func TestDownloadStructuredArgsRedactionAndStates(t *testing.T) {
	t.Setenv("HF_HOME", "/inherited/cache")
	r := &fakeRunner{cmd: &fakeCmd{out: "50% token-secret\n100%\n"}}
	d := New("hf", r)
	if _, e := d.DownloadRequest(context.Background(), Request{ID: "one", Repository: "org/model", Revision: "refs/pr/2", Destination: "/models/m", Token: "token-secret"}); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for {
		j, _ := d.Status("one")
		if j.State == Succeeded {
			if strings.Contains(strings.Join(j.Logs, ""), "token-secret") {
				t.Fatal("secret leaked")
			}
			wantArgs := []string{"download", "org/model", "--local-dir", "/models/m", "--revision", "refs/pr/2"}
			if r.name != "hf" || strings.Join(r.args, "|") != strings.Join(wantArgs, "|") || strings.Contains(strings.Join(r.args, " "), "token-secret") {
				t.Fatalf("args %#v", r.args)
			}
			if !contains(r.cmd.env, "HF_TOKEN=token-secret") {
				t.Fatalf("token not passed in environment: %#v", r.cmd.env)
			}
			if !contains(r.cmd.env, "HF_HOME=/inherited/cache") {
				t.Fatalf("inherited HF_HOME lost: %#v", r.cmd.env)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(time.Millisecond)
	}
	r.cmd = &fakeCmd{wait: errors.New("exit 1")}
	if _, e := d.Download(context.Background(), "two", "org/model", "/m", ""); e != nil {
		t.Fatal(e)
	}
	for {
		j, _ := d.Status("two")
		if j.State == Failed {
			break
		}
		time.Sleep(time.Millisecond)
	}
}

func TestConfiguredHFHomeOverridesInheritedValue(t *testing.T) {
	t.Setenv("HF_HOME", "/inherited")
	r := &fakeRunner{cmd: &fakeCmd{}}
	d := New("hf", r)
	d.SetHFHome("/configured")
	if _, err := d.Download(context.Background(), "home", "org/model", "/models/home", ""); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d, "home", Succeeded)
	if !contains(r.cmd.env, "HF_HOME=/configured") || contains(r.cmd.env, "HF_HOME=/inherited") {
		t.Fatalf("environment %#v", r.cmd.env)
	}
}

func TestRegisteredModelDownloadLifecycleAndRestore(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "model")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "weights"), []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.DB.Exec(`INSERT INTO models(id,kind,source,local_path,created_at,name,repository,revision,size_bytes,status,updated_at) VALUES('model-id','huggingface','org/model',NULL,?,'model','org/model','main',0,'registered',?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	d := New("hf", &fakeRunner{cmd: &fakeCmd{}})
	d.SetRoot(root)
	d.SetStore(s)
	j, err := d.DownloadRequest(context.Background(), Request{ID: "linked", ModelID: "model-id", Repository: "org/model", Destination: destination})
	if err != nil {
		t.Fatal(err)
	}
	if j.Revision != "main" {
		t.Fatalf("revision %q", j.Revision)
	}
	waitForState(t, d, "linked", Succeeded)
	var status, path string
	var size int64
	if err = s.DB.QueryRow(`SELECT status,COALESCE(local_path,''),size_bytes FROM models WHERE id='model-id'`).Scan(&status, &path, &size); err != nil {
		t.Fatal(err)
	}
	if status != "ready" || path != destination || size != 5 {
		t.Fatalf("model state %q %q %d", status, path, size)
	}
	var modelID, revision string
	if err = s.DB.QueryRow(`SELECT COALESCE(model_id,''),revision FROM downloads WHERE id='linked'`).Scan(&modelID, &revision); err != nil {
		t.Fatal(err)
	}
	if modelID != "model-id" || revision != "main" {
		t.Fatalf("persisted relationship %q %q", modelID, revision)
	}
	_, err = s.DB.Exec(`UPDATE downloads SET state='running',finished_at=NULL WHERE id='linked'; UPDATE models SET status='downloading' WHERE id='model-id'`)
	if err != nil {
		t.Fatal(err)
	}
	restored := New("hf", &fakeRunner{cmd: &fakeCmd{}})
	restored.SetStore(s)
	waitForState(t, restored, "linked", Canceled)
	if err = s.DB.QueryRow(`SELECT status FROM models WHERE id='model-id'`).Scan(&status); err != nil || status != "canceled" {
		t.Fatalf("restored model status %q err=%v", status, err)
	}
}

func waitForState(t *testing.T, d *Downloader, id string, want State) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if job, ok := d.Status(id); ok && job.State == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("job %q did not reach %q", id, want)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type unsafeCmd struct{}

func (*unsafeCmd) StdoutPipe() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (*unsafeCmd) StderrPipe() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (*unsafeCmd) Start() error { return nil }
func (*unsafeCmd) Wait() error  { return nil }

type unsafeRunner struct{ cmd *unsafeCmd }

func (r *unsafeRunner) CommandContext(context.Context, string, ...string) Command { return r.cmd }

func TestTokenRequiresEnvironmentCapableRunner(t *testing.T) {
	d := New("hf", &unsafeRunner{cmd: &unsafeCmd{}})
	if _, err := d.Download(context.Background(), "one", "org/model", "/models/m", "secret"); err == nil || !strings.Contains(err.Error(), "securely") {
		t.Fatalf("unsafe runner result: %v", err)
	}
	if len(d.List()) != 0 {
		t.Fatal("unsafe request created a job")
	}
}

func TestListOrderingAndPersistedRestore(t *testing.T) {
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, row := range []struct{ id, state string }{{"z", "succeeded"}, {"a", "running"}} {
		_, err = s.DB.Exec(`INSERT INTO downloads(id,repository,destination,state,progress,error,logs,created_at,updated_at) VALUES(?,?,?,?,0,'','[]',?,?)`, row.id, "org/model", "/models/"+row.id, row.state, now, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	d := New("hf", &fakeRunner{cmd: &fakeCmd{}})
	d.SetStore(s)
	jobs := d.List()
	if len(jobs) != 2 || jobs[0].ID != "a" || jobs[0].State != Canceled || jobs[1].ID != "z" || jobs[1].State != Succeeded {
		t.Fatalf("restored jobs %#v", jobs)
	}
}

func TestConcurrentDestinationRejected(t *testing.T) {
	d := New("hf", &fakeRunner{cmd: &fakeCmd{}})
	d.jobs["busy"] = &Job{ID: "busy", Destination: "/models/same", State: Running}
	if _, err := d.Download(context.Background(), "other", "org/model", "/models/same", ""); err == nil || !strings.Contains(err.Error(), "destination") {
		t.Fatalf("same destination result: %v", err)
	}
}

type lifecycleRunner struct {
	started chan struct{}
}

func (r *lifecycleRunner) CommandContext(ctx context.Context, _ string, _ ...string) Command {
	return &lifecycleCommand{ctx: ctx, started: r.started}
}

type lifecycleCommand struct {
	ctx     context.Context
	started chan struct{}
}

func (c *lifecycleCommand) StdoutPipe() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (c *lifecycleCommand) StderrPipe() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (c *lifecycleCommand) Start() error {
	close(c.started)
	return nil
}
func (c *lifecycleCommand) Wait() error {
	<-c.ctx.Done()
	return c.ctx.Err()
}

func TestDownloadOutlivesRequestAndShutdownPersistsCancellation(t *testing.T) {
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	runner := &lifecycleRunner{started: make(chan struct{})}
	d := New("hf", runner)
	d.SetStore(st)
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	if _, err = d.Download(requestCtx, "service-owned", "org/model", "/models/service-owned", ""); err != nil {
		t.Fatal(err)
	}
	<-runner.started
	cancelRequest()
	time.Sleep(20 * time.Millisecond)
	if job, ok := d.Status("service-owned"); !ok || job.State != Running {
		t.Fatalf("request cancellation terminated accepted download: %#v", job)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), time.Second)
	defer cancelShutdown()
	if err = d.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	job, ok := d.Status("service-owned")
	if !ok || job.State != Canceled || !strings.Contains(job.Error, "canceled") {
		t.Fatalf("shutdown state %#v", job)
	}
	var state string
	if err = st.DB.QueryRow(`SELECT state FROM downloads WHERE id='service-owned'`).Scan(&state); err != nil || state != string(Canceled) {
		t.Fatalf("persisted shutdown state %q err=%v", state, err)
	}
	if _, err = d.Download(context.Background(), "late", "org/model", "/models/late", ""); err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Fatalf("post-shutdown start result: %v", err)
	}
}
