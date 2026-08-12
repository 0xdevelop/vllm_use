package runtime

import (
	"reflect"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	got, e := BuildArgs(Options{Model: "m", Host: "127.0.0.1", Port: 8000, TensorParallel: 2, ExtraArgs: []ExtraArg{{Name: "dtype", Values: []string{"float16"}}}})
	if e != nil {
		t.Fatal(e)
	}
	want := []string{"serve", "m", "--host", "127.0.0.1", "--port", "8000", "--tensor-parallel-size", "2", "--dtype", "float16"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
	if _, e = BuildArgs(Options{Model: "m", Port: 1, ExtraArgs: []ExtraArg{{Name: "port", Values: []string{"2"}}}}); e == nil {
		t.Fatal("reserved arg accepted")
	}
}
