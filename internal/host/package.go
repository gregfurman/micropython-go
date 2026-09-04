package host

import (
	"fmt"
	"sort"

	"github.com/gregfurman/micropython-go/internal/host/codec"
	"github.com/gregfurman/micropython-go/internal/host/memory"
	"github.com/gregfurman/micropython-go/internal/value"
)

// Package is a tree of ordinary Python modules that the host installs into an
// interpreter. Functions remain owned by the Go registry; Values are encoded
// into the guest module dictionaries.
type Package struct {
	Name      string
	Functions map[string]HostFunc
	Values    map[string]value.Value
	Packages  []Package
}

// RegisterPackage installs pkg and all its children.
func (i *Module) RegisterPackage(pkg Package) error {
	return i.installPackage("", pkg)
}

func (i *Module) installPackage(parent string, pkg Package) error {
	path := pkg.Name
	if parent != "" {
		path = parent + "." + path
	}

	if err := i.defineModule(path); err != nil {
		return err
	}

	for _, name := range sortedKeys(pkg.Values) {
		if err := i.setModuleAttr(path, name, pkg.Values[name]); err != nil {
			return fmt.Errorf("package %s attribute %s: %w", path, name, err)
		}
	}

	for _, name := range sortedKeys(pkg.Functions) {
		if err := i.defineModuleFunction(path, name, pkg.Functions[name]); err != nil {
			return fmt.Errorf("package %s function %s: %w", path, name, err)
		}
	}

	for _, child := range pkg.Packages {
		if err := i.installPackage(path, child); err != nil {
			return err
		}
	}
	return nil
}

func (i *Module) defineModule(path string) error {
	ptr, free, err := i.mem.WriteString(path)
	if err != nil {
		return err
	}
	defer free()

	i.mod.Xdefine_module(ptr, int32(len(path)), i.scratch)
	_, err = i.consume(i.scratch)
	return err
}

func (i *Module) defineModuleFunction(path, name string, fn HostFunc) error {
	if fn == nil {
		return fmt.Errorf("nil host function")
	}

	pathPtr, freePath, err := i.mem.WriteString(path)
	if err != nil {
		return err
	}
	defer freePath()
	namePtr, freeName, err := i.mem.WriteString(name)
	if err != nil {
		return err
	}
	defer freeName()

	i.mod.Xdefine_module_function(pathPtr, int32(len(path)), namePtr, int32(len(name)), i.register(fn), i.scratch)
	_, err = i.consume(i.scratch)
	return err
}

func (i *Module) setModuleAttr(path, name string, v value.Value) error {
	pathPtr, freePath, err := i.mem.WriteString(path)
	if err != nil {
		return err
	}
	defer freePath()
	namePtr, freeName, err := i.mem.WriteString(name)
	if err != nil {
		return err
	}
	defer freeName()

	valuePtr := i.mem.Alloc(codec.ValueSize)
	if valuePtr == 0 {
		return memory.ErrGuestOOM
	}
	defer i.mem.Free(valuePtr)
	if err := i.codec.Encode(valuePtr, v); err != nil {
		return err
	}

	i.mod.Xset_module_attr(pathPtr, int32(len(path)), namePtr, int32(len(name)), valuePtr, i.scratch)
	_, err = i.consume(i.scratch)
	return err
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
