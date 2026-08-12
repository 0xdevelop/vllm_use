package download

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xdevelop/vllm-use/internal/store"
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
	r := &fakeRunner{cmd: &fakeCmd{out: "50% token-secret\n100%\n"}}
	d := New("hf", r)
	if _, e := d.Download(context.Background(), "one", "org/model", "/models/m", "token-secret"); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for {
		j, _ := d.Status("one")
		if j.State == Succeeded {
			if strings.Contains(strings.Join(j.Logs, ""), "token-secret") {
				t.Fatal("secret leaked")
			}
			if r.name != "hf" || len(r.args) != 4 || r.args[0] != "download" || strings.Contains(strings.Join(r.args, " "), "token-secret") {
				t.Fatalf("args %#v", r.args)
			}
			if !contains(r.cmd.env, "HF_TOKEN=token-secret") {
				t.Fatalf("token not passed in environment: %#v", r.cmd.env)
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
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
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
