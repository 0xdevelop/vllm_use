package gpu

import (
	"context"
	"errors"
	"testing"
)

type fake struct {
	out string
	err error
}

func (f fake) Output(context.Context, string, ...string) ([]byte, error) { return []byte(f.out), f.err }
func TestList(t *testing.T) {
	g, e := New(fake{out: "0, RTX 4090, uuid, 24564, 100\n"}).List(context.Background())
	if e != nil || len(g) != 1 || g[0].MemoryTotalMiB != 24564 {
		t.Fatalf("%v %v", g, e)
	}
	g, e = New(fake{err: errors.New("missing")}).List(context.Background())
	if e != nil || len(g) != 0 {
		t.Fatalf("degrade: %v %v", g, e)
	}
}
