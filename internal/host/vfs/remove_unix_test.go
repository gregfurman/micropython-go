//go:build unix

package vfs

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/gregfurman/micropython-go/internal/host/abi"
)

// This fixture only deletes immediate children of a test directory. It uses
// atomic OS operations, not a stat-then-Remove approximation of the contract.
type removalFS struct {
	fs.FS
	dir string
}

func (f removalFS) target(name string) (string, error) {
	if !fs.ValidPath(name) || name == "." || strings.Contains(name, "/") {
		return "", fs.ErrInvalid
	}
	return filepath.Join(f.dir, name), nil
}

func (f removalFS) Unlink(name string) error {
	name, err := f.target(name)
	if err != nil {
		return err
	}
	return syscall.Unlink(name)
}

func (f removalFS) Rmdir(name string) error {
	name, err := f.target(name)
	if err != nil {
		return err
	}
	return syscall.Rmdir(name)
}

func TestRemovalDoesNotFollowFinalSymlink(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.Mkdir("directory", 0700); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("file", []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"link": "directory", "dangling": "missing"} {
		if err := root.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}
	f := newTestFS(t, removalFS{FS: writableFS{root}, dir: dir})
	size := put(t, f, 0, "file")
	want(t, f.Xhost_fs_rmdir(0, size), -abi.ENOTDIR)
	for _, name := range []string{"file/", "file/.", "./file/"} {
		size := put(t, f, 0, name)
		want(t, f.Xhost_fs_remove(0, size), -abi.EINVAL)
	}
	if data, err := root.ReadFile("file"); err != nil || string(data) != "original" {
		t.Fatalf("rejected deletion changed file: %q, %v", data, err)
	}
	for _, name := range []string{"link", "dangling"} {
		size := put(t, f, 0, name)
		want(t, f.Xhost_fs_rmdir(0, size), -abi.ENOTDIR)
		if _, err := root.Lstat(name); err != nil {
			t.Fatalf("rmdir removed symlink %s: %v", name, err)
		}
		want(t, f.Xhost_fs_remove(0, size), 0)
	}
	size = put(t, f, 0, "directory")
	if got := f.Xhost_fs_remove(0, size); got >= 0 {
		t.Fatal("unlink removed a directory")
	}
	want(t, f.Xhost_fs_rmdir(0, size), 0)
	size = put(t, f, 0, "file")
	want(t, f.Xhost_fs_remove(0, size), 0)
}
