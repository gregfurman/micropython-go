package micropython

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

func TestWithFS(t *testing.T) {
	ctx := context.Background()
	backend := fstest.MapFS{
		"hello.txt":   {Data: []byte("hello")},
		"greeting.py": {Data: []byte("message = 'from filesystem'\n")},
	}
	in, err := NewInstance(ctx, WithFS(backend))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close() })
	if err := in.Exec(ctx, `
with open('/hello.txt') as f:
    assert f.read() == 'hello'
import greeting
assert greeting.message == 'from filesystem'
`); err != nil {
		t.Fatal(err)
	}
	clone, err := in.Clone(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clone.Close() })
	if err := clone.Exec(ctx, "with open('hello.txt') as f:\n    assert f.read() == 'hello'"); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{
		"open('../hello.txt')", "open('/hello.txt', 'w')", "open('missing')",
	} {
		if err := in.Exec(ctx, src); err == nil {
			t.Errorf("unexpectedly permitted %s", src)
		}
	}
}

func TestFilesystemDeniedByDefault(t *testing.T) {
	ctx := context.Background()
	in := newT(t)
	if err := in.Exec(ctx, "open('/etc/passwd')"); err == nil {
		t.Fatal("unconfigured filesystem was accessible")
	}
	backend := fstest.MapFS{"hello": {Data: []byte("hello")}}
	denied, err := NewInstance(ctx, WithFS(backend), WithFS(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer denied.Close()
	if err := denied.Exec(ctx, "open('hello')"); err == nil {
		t.Fatal("last WithFS did not replace mount")
	}
}

type writableRootFS struct{ *os.Root }

func (f writableRootFS) Open(name string) (fs.File, error) { return f.Root.Open(name) }

func (f writableRootFS) OpenFile(name string, flags int, perm fs.FileMode) (fs.File, error) {
	return f.Root.OpenFile(name, flags, perm)
}

func TestWithWritableFS(t *testing.T) {
	ctx := context.Background()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	backend := writableRootFS{root}
	in, err := NewInstance(ctx, WithFS(backend))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if err := in.Exec(ctx, `
with open('/result.txt', 'w+') as f:
    assert f.write('hello') == 5
    assert f.tell() == 5
    f.seek(0)
    assert f.read() == 'hello'
    f.flush()
with open('/result.txt', 'a+') as f:
    assert f.tell() == 5
    f.write('!')
with open('/result.txt', 'rb') as f:
    assert f.read() == b'hello!'
`); err != nil {
		t.Fatal(err)
	}
	program, err := Compile(ctx, "", WithFS(backend))
	if err != nil {
		t.Fatal(err)
	}
	defer program.Close()
	if err := program.Run(ctx, func(ctx context.Context, in *OwnedInstance) error {
		return in.Exec(ctx, "f = open('result.txt', 'a')\nf.write('!')")
	}); err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(backend, "result.txt")
	if err != nil || string(data) != "hello!!" {
		t.Fatalf("persistent write = %q, %v", data, err)
	}
}

type trackedFS struct {
	fs.FS
	live     atomic.Int32
	closeErr error
}

func (f *trackedFS) Open(name string) (fs.File, error) {
	file, err := f.FS.Open(name)
	if err != nil {
		return nil, err
	}
	f.live.Add(1)
	return &trackedFile{File: file, owner: f}, nil
}

type trackedFile struct {
	fs.File
	owner *trackedFS
}

func (f *trackedFile) Close() error {
	f.owner.live.Add(-1)
	return errors.Join(f.File.Close(), f.owner.closeErr)
}

func TestProgramReportsFileCleanupErrors(t *testing.T) {
	commitErr := errors.New("commit failed")
	callbackErr := errors.New("callback failed")
	for _, disposition := range []string{"rewind", "discard", "closed program"} {
		for _, result := range []error{nil, callbackErr} {
			t.Run(disposition+"/"+fmt.Sprint(result), func(t *testing.T) {
				backend := &trackedFS{FS: fstest.MapFS{"file": {}}, closeErr: commitErr}
				p, err := Compile(t.Context(), "", WithFS(backend))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = p.Close() })
				if disposition == "discard" {
					p.maxIdle = 0
				}
				err = p.Run(t.Context(), func(ctx context.Context, in *OwnedInstance) error {
					if err := in.Exec(ctx, "f = open('file')"); err != nil {
						return err
					}
					if disposition == "closed program" {
						if err := p.Close(); err != nil {
							return err
						}
					}
					return result
				})
				if !errors.Is(err, commitErr) || (result != nil && !errors.Is(err, result)) {
					t.Fatalf("Run = %v, want cleanup error and callback error %v", err, result)
				}
				if backend.live.Load() != 0 || len(p.free) != 0 {
					t.Fatal("failed cleanup left a live file or pooled interpreter")
				}
			})
		}
	}
}

func TestProgramCleanupPreservesPanic(t *testing.T) {
	backend := &trackedFS{FS: fstest.MapFS{"file": {}}, closeErr: errors.New("close failed")}
	p, err := Compile(t.Context(), "", WithFS(backend))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	defer func() {
		if got := recover(); got != "callback panic" {
			t.Errorf("panic = %v", got)
		}
		if backend.live.Load() != 0 || len(p.free) != 0 {
			t.Error("panic left a live file or pooled interpreter")
		}
	}()
	_ = p.Run(t.Context(), func(ctx context.Context, in *OwnedInstance) error {
		if err := in.Exec(ctx, "f = open('file')"); err != nil {
			t.Fatal(err)
		}
		panic("callback panic")
	})
}

func TestProgramCloseReportsFileErrors(t *testing.T) {
	commitErr := errors.New("commit failed")
	backend := &trackedFS{FS: fstest.MapFS{"file": {}}, closeErr: commitErr}
	p, err := Compile(t.Context(), "", WithFS(backend))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	// Exercise Close's aggregation directly. Ordinary runs close their files
	// before returning an interpreter to the idle pool.
	if err := p.free[0].Exec(t.Context(), "f = open('file')"); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); !errors.Is(err, commitErr) {
		t.Fatalf("Close = %v, want %v", err, commitErr)
	}
	if backend.live.Load() != 0 {
		t.Fatal("Close leaked a file")
	}
}

func TestReadOnlyFS(t *testing.T) {
	if ReadOnly(nil) != nil {
		t.Fatal("ReadOnly(nil) must preserve denied access")
	}
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.WriteFile("file", []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	in, err := NewInstance(t.Context(), WithFS(ReadOnly(writableRootFS{root})))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if err := in.Exec(t.Context(), "with open('file') as f:\n    assert f.read() == 'original'"); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"open('file', 'w')", "import os; os.mkdir('dir')", "import os; os.rename('file', 'renamed')"} {
		if err := in.Exec(t.Context(), src); err == nil {
			t.Errorf("ReadOnly allowed %s", src)
		}
	}
}

func TestOpenRejectsInvalidModesWithoutTruncating(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.WriteFile("file", []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	in, err := NewInstance(t.Context(), WithFS(writableRootFS{root}))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if err := in.Exec(t.Context(), `
for mode in ('rw', 'rr', 'w++', 'rbt', '+', '', 'r\x00w'):
    try:
        open('file', mode)
    except ValueError:
        pass
    else:
        raise AssertionError(mode)
with open('file') as f:
    assert f.read() == 'original'
`); err != nil {
		t.Fatal(err)
	}
}

func TestFilesystemLifecycle(t *testing.T) {
	ctx := context.Background()
	backend := &trackedFS{FS: fstest.MapFS{"hello": {Data: []byte("hello")}}}
	in, err := NewInstance(ctx, WithFS(backend))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close() })
	if err := in.Exec(ctx, "f = open('hello')"); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Clone(ctx); err == nil || !strings.Contains(err.Error(), "close open files") {
		t.Fatalf("Clone = %v", err)
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	if backend.live.Load() != 0 {
		t.Fatal("Close leaked an open file")
	}
	program, err := Compile(ctx, "", WithFS(backend))
	if err != nil {
		t.Fatal(err)
	}
	defer program.Close()
	for range 2 {
		if err := program.Run(ctx, func(ctx context.Context, in *OwnedInstance) error {
			return in.Exec(ctx, "f = open('hello')\nassert f.read() == 'hello'")
		}); err != nil {
			t.Fatal(err)
		}
		if backend.live.Load() != 0 {
			t.Fatal("Run leaked an open file")
		}
	}
	if p, err := Compile(ctx, "f = open('hello')", WithFS(backend)); err == nil {
		p.Close()
		t.Fatal("Compile captured an open file")
	}
	if backend.live.Load() != 0 {
		t.Fatal("failed Compile leaked an open file")
	}
}
