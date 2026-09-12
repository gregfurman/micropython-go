package vfs

import (
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gregfurman/micropython-go/internal/host/abi"
	"github.com/gregfurman/micropython-go/internal/host/memory"
)

func newTestFS(t *testing.T, backend fs.FS) *Filesystem {
	t.Helper()

	f := New(memory.New(1, 2), backend)

	t.Cleanup(func() { _ = f.Close() })

	return f
}

func put(t *testing.T, f *Filesystem, ptr int32, value string) int32 {
	t.Helper()

	if err := f.mem.Write(ptr, []byte(value)); err != nil {
		t.Fatal(err)
	}

	return int32(len(value))
}

func want(t *testing.T, got, expected int32) {
	t.Helper()

	if got != expected {
		t.Fatalf("got %d, want %d", got, expected)
	}
}

func TestReadOnlyFS(t *testing.T) {
	f := newTestFS(t, fstest.MapFS{"hello.txt": {Data: []byte("hello"), ModTime: time.Unix(123, 0)}})
	size := put(t, f, 0, "/hello.txt")
	want(t, f.Xhost_fs_stat(0, size, 64), 0)

	stat, _ := f.mem.View(64, 12)
	if binary.LittleEndian.Uint32(stat) != 0100000 || binary.LittleEndian.Uint32(stat[4:]) != 5 || binary.LittleEndian.Uint32(stat[8:]) != 123 {
		t.Fatalf("stat = %v", stat)
	}

	fd := f.Xhost_fs_open(0, size, read)
	want(t, fd, 0)
	want(t, f.Xhost_fs_read(fd, 128, 10), 5)

	b, _ := f.mem.View(128, 5)
	if string(b) != "hello" {
		t.Fatal(string(b))
	}

	want(t, f.Xhost_fs_read(fd, 128, 10), 0)
	want(t, f.Xhost_fs_write(fd, 128, 5), -abi.EBADF)
	want(t, f.Xhost_fs_flush(fd), 0)
	want(t, f.Xhost_fs_open(0, size, write|truncate), -abi.EROFS)
	want(t, f.Xhost_fs_mkdir(0, size), -abi.EROFS)
	want(t, f.Xhost_fs_remove(0, size), -abi.EROFS)
	want(t, f.Xhost_fs_rmdir(0, size), -abi.EROFS)
	want(t, f.Xhost_fs_rename(0, size, 0, size), -abi.EROFS)
	want(t, f.Xhost_fs_close(fd), 0)
	want(t, f.Xhost_fs_close(fd), -abi.EBADF)
}

// A backend grants each mutation separately, so Unlink does not imply Rmdir.
type unlinkOnlyFS struct {
	fs.FS
	unlinked string
}

func (f *unlinkOnlyFS) Unlink(name string) error { f.unlinked = name; return nil }

func TestCapabilitiesAreIndependent(t *testing.T) {
	backend := &unlinkOnlyFS{FS: fstest.MapFS{"a": {Data: []byte("a")}}}
	f := newTestFS(t, backend)
	size := put(t, f, 0, "a")
	want(t, f.Xhost_fs_remove(0, size), 0)

	if backend.unlinked != "a" {
		t.Fatalf("unlinked %q", backend.unlinked)
	}

	want(t, f.Xhost_fs_rmdir(0, size), -abi.EROFS)
	want(t, f.Xhost_fs_mkdir(0, size), -abi.EROFS)
	want(t, f.Xhost_fs_rename(0, size, 0, size), -abi.EROFS)
	want(t, f.Xhost_fs_open(0, size, write), -abi.EROFS)

	// The mount root is a path to read, never one to create or destroy.
	root := put(t, f, 0, "/")
	want(t, f.Xhost_fs_remove(0, root), -abi.EACCES)
	want(t, f.Xhost_fs_rmdir(0, root), -abi.EACCES)
	want(t, f.Xhost_fs_mkdir(0, root), -abi.EACCES)
	want(t, f.Xhost_fs_rename(0, root, 0, size), -abi.EACCES)
	want(t, f.Xhost_fs_stat(0, root, 64), 0)
}

type countingFS struct {
	fs.FS
	opens int
}

func (f *countingFS) Open(name string) (fs.File, error) { f.opens++; return f.FS.Open(name) }

func TestValidateBeforeOpening(t *testing.T) {
	backend := &countingFS{FS: fstest.MapFS{"safe": {Data: []byte("data")}}}

	f := newTestFS(t, backend)
	for _, name := range []string{"../safe", "/../safe", "dir/../safe", "safe\x00", "dir\\safe", string([]byte{255})} {
		size := put(t, f, 0, name)
		if got := f.Xhost_fs_open(0, size, read); got >= 0 {
			t.Errorf("accepted %q", name)
		}
	}

	size := put(t, f, 0, "safe")
	for _, flags := range []int32{0, read | truncate, write | appendMode | truncate, 128} {
		want(t, f.Xhost_fs_open(0, size, flags), -abi.EINVAL)
	}

	want(t, f.Xhost_fs_open(65535, 4, read), -abi.EFAULT)
	want(t, f.Xhost_fs_stat(0, size, 65535), -abi.EFAULT)
	f.next = math.MaxInt32 + 1
	want(t, f.Xhost_fs_open(0, size, read), -abi.EMFILE)

	if backend.opens != 0 {
		t.Fatalf("opened %d times before validation", backend.opens)
	}

	denied := newTestFS(t, nil)
	want(t, denied.Xhost_fs_open(0, 0, read), -abi.EACCES)
}

func TestDirectoryIteration(t *testing.T) {
	f := newTestFS(t, fstest.MapFS{"a": {Data: []byte("a")}, "dir/b": {Data: []byte("b")}})
	size := put(t, f, 0, "/")
	dir := f.Xhost_fs_opendir(0, size)
	want(t, dir, 0)
	want(t, f.Xhost_fs_readdir(dir, 128, 255, 65535), -abi.EFAULT)
	want(t, f.Xhost_fs_readdir(dir, 128, 0, 64), -abi.ENAMETOOLONG)
	want(t, f.Xhost_fs_readdir(dir, 128, 255, 64), 1)

	b, _ := f.mem.View(128, 1)
	if string(b) != "a" {
		t.Fatal(string(b))
	}

	want(t, f.Xhost_fs_readdir(dir, 128, 255, 64), 3)

	mode, _ := f.mem.View(64, 4)
	if binary.LittleEndian.Uint32(mode) != 0040000 {
		t.Fatal("directory mode missing")
	}

	want(t, f.Xhost_fs_readdir(dir, 128, 255, 64), 0)
	want(t, f.Xhost_fs_read(dir, 128, 2), -abi.EBADF)
	want(t, f.Xhost_fs_closedir(dir), 0)
	want(t, f.Xhost_fs_closedir(dir), -abi.EBADF)
	size = put(t, f, 0, "a")
	want(t, f.Xhost_fs_opendir(0, size), -abi.ENOTDIR)
}

// os.Root supplies confinement; this adapter preserves fs.FS method signatures.
type writableFS struct{ *os.Root }

func (f writableFS) Open(name string) (fs.File, error) { return f.Root.Open(name) }
func (f writableFS) OpenFile(name string, flags int, perm fs.FileMode) (fs.File, error) {
	return f.Root.OpenFile(name, flags, perm)
}

// The assertions a backend author writes to catch a mismatched signature at
// compile time instead of as an EROFS from Python.
var (
	_ fs.FS      = writableFS{}
	_ OpenFileFS = writableFS{}
	_ MkdirFS    = writableFS{}
	_ RenameFS   = writableFS{}
)

func TestWritableFS(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = root.Close() })
	f := newTestFS(t, writableFS{root})
	size := put(t, f, 0, "dir")
	want(t, f.Xhost_fs_mkdir(0, size), 0)
	want(t, f.Xhost_fs_mkdir(0, size), -abi.EEXIST)
	size = put(t, f, 0, "missing/child")
	want(t, f.Xhost_fs_mkdir(0, size), -abi.ENOENT)
	size = put(t, f, 0, "dir/file")
	fd := f.Xhost_fs_open(0, size, read|write|create|truncate)
	want(t, fd, 0)
	put(t, f, 128, "hello")
	want(t, f.Xhost_fs_write(fd, 128, 5), 5)
	want(t, f.Xhost_fs_seek(fd, 0, io.SeekStart, 65535), -abi.EFAULT)
	want(t, f.Xhost_fs_seek(fd, 0, io.SeekCurrent, 64), 0)

	position, _ := f.mem.View(64, 4)
	if binary.LittleEndian.Uint32(position) != 5 {
		t.Fatal("invalid seek changed position")
	}

	want(t, f.Xhost_fs_seek(fd, 0, io.SeekStart, 64), 0)
	want(t, f.Xhost_fs_read(fd, 256, 5), 5)
	want(t, f.Xhost_fs_flush(fd), 0)
	want(t, f.Xhost_fs_close(fd), 0)
	fd = f.Xhost_fs_open(0, size, write|appendMode)
	want(t, f.Xhost_fs_write(fd, 128, 5), 5)
	want(t, f.Xhost_fs_close(fd), 0)

	data, err := fs.ReadFile(writableFS{root}, "dir/file")
	if err != nil || string(data) != "hellohello" {
		t.Fatalf("%q, %v", data, err)
	}

	newSize := put(t, f, 32, "dir/renamed")
	want(t, f.Xhost_fs_rename(0, size, 32, newSize), 0)
	// os.Root.Remove grants neither deletion capability: it cannot atomically
	// distinguish unlink from rmdir. Both must stay denied through this adapter.
	want(t, f.Xhost_fs_remove(32, newSize), -abi.EROFS)
	want(t, f.Xhost_fs_rmdir(32, newSize), -abi.EROFS)
}

func TestConfinedBackendRejectsEscapingSymlink(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(outside+"/secret", []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := root.Symlink(outside+"/secret", "escape"); err != nil {
		t.Fatal(err)
	}

	f := newTestFS(t, writableFS{root})

	size := put(t, f, 0, "escape")
	if fd := f.Xhost_fs_open(0, size, read); fd >= 0 {
		t.Fatal("escaped filesystem root")
	}
}

func TestRejectedSeekPreservesPosition(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := root.WriteFile("file", []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	f := newTestFS(t, writableFS{root})
	size := put(t, f, 0, "file")
	fd := f.Xhost_fs_open(0, size, read)
	want(t, fd, 0)

	file, _ := f.file(fd)
	for _, tc := range []struct {
		position, offset, whence, status int32
	}{
		{2, math.MaxInt32, io.SeekCurrent, -abi.EFBIG},
		{2, math.MaxInt32, io.SeekEnd, -abi.EFBIG},
		{2, -1, io.SeekStart, -abi.EINVAL},
		{2, -3, io.SeekCurrent, -abi.EINVAL},
		{math.MaxInt32, 1, io.SeekCurrent, -abi.EFBIG},
	} {
		want(t, f.Xhost_fs_seek(fd, tc.position, io.SeekStart, 64), 0)

		file.readErr = syscall.EIO

		put(t, f, 64, "keep")
		want(t, f.Xhost_fs_seek(fd, tc.offset, tc.whence, 64), tc.status)

		position, err := file.File.(io.Seeker).Seek(0, io.SeekCurrent)
		if err != nil || position != int64(tc.position) || file.readErr != syscall.EIO {
			t.Fatalf("rejected seek changed state: position=%d, readErr=%v, err=%v", position, file.readErr, err)
		}

		out, _ := f.mem.View(64, 4)
		if string(out) != "keep" {
			t.Fatal("rejected seek wrote its output")
		}
	}
}

type partialFile struct {
	fs.File
	reads, closes int
}

func (f *partialFile) Read(b []byte) (int, error) { f.reads++; return copy(b, "x"), syscall.EIO }
func (f *partialFile) Close() error               { f.closes++; return nil }

func TestPartialReadsAndReset(t *testing.T) {
	f := newTestFS(t, fstest.MapFS{})
	opened := &partialFile{}
	fd := f.add(&openFile{File: opened, flags: read})
	want(t, f.Xhost_fs_read(fd, 65535, 2), -abi.EFAULT)
	want(t, f.Xhost_fs_read(fd, 128, 2), 1)
	want(t, f.Xhost_fs_read(fd, 128, 2), -abi.EIO)

	if opened.reads != 1 {
		t.Fatal("partial error lost or invalid read performed")
	}

	if err := f.Reset(fstest.MapFS{}); err != nil {
		t.Fatal(err)
	}

	if opened.closes != 1 || f.HasOpenFiles() {
		t.Fatal("reset leaked a file")
	}

	want(t, f.Xhost_fs_close(fd), -abi.EBADF)

	next := f.add(&openFile{File: &partialFile{}})
	if next <= fd {
		t.Fatal("reused stale descriptor")
	}
}

func TestLongNamesAndHandleLimit(t *testing.T) {
	name := strings.Repeat("a", 256)
	f := newTestFS(t, fstest.MapFS{name: {}})
	dir := f.Xhost_fs_opendir(0, 0)
	want(t, f.Xhost_fs_readdir(dir, 128, 255, 64), -abi.ENAMETOOLONG)
	want(t, f.Xhost_fs_closedir(dir), 0)

	size := put(t, f, 0, name)
	for range maxHandles {
		if fd := f.Xhost_fs_open(0, size, read); fd < 0 {
			t.Fatal(fd)
		}
	}

	want(t, f.Xhost_fs_open(0, size, read), -abi.EMFILE)

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	want(t, f.Xhost_fs_open(0, size, read), -abi.EBADF)
}

func TestErrnos(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int32
	}{
		{&fs.PathError{Op: "open", Path: "x", Err: fs.ErrNotExist}, -abi.ENOENT},
		{fs.ErrPermission, -abi.EACCES}, {syscall.EROFS, -abi.EROFS},
		{syscall.ENOTEMPTY, -abi.ENOTEMPTY}, {errors.New("unknown"), -abi.EIO},
	} {
		want(t, errnoOf(tc.err), tc.want)
	}
}
