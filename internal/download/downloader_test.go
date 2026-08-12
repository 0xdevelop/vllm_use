package download

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	cmd  *fakeCmd
	name string
	args []string
}

func (f *fakeRunner) CommandContext(_ context.Context, n string, a ...string) Command {
	f.name = n
	f.args = append([]string(nil), a...)
	return f.cmd
}

type fakeCmd struct {
	out, errout string
	wait        error
}

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
			if r.name != "hf" || len(r.args) != 6 || r.args[0] != "download" {
				t.Fatalf("args %#v", r.args)
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
