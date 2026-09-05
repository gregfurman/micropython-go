package host

import (
	"fmt"
	"maps"
	"slices"

	"github.com/gregfurman/micropython-go/internal/host/codec"
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

	for _, name := range slices.Sorted(maps.Keys(pkg.Values)) {
		if err := i.setModuleAttr(path, name, pkg.Values[name]); err != nil {
			return fmt.Errorf("package %s attribute %s: %w", path, name, err)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(pkg.Functions)) {
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
	defer i.arena.Mark()()

	ptr, err := i.arena.String(path)
	if err != nil {
		return err
	}

	outPtr, err := i.result()
	if err != nil {
		return err
	}

	used := i.mod.Xdefine_module(ptr, int32(len(path)), outPtr, defaultValueArenaCapacity)
	_, err = i.consumeArena(outPtr, used)
	return err
}

func (i *Module) defineModuleFunction(path, name string, fn HostFunc) error {
	if fn == nil {
		return fmt.Errorf("nil host function")
	}

	defer i.arena.Mark()()

	pathPtr, err := i.arena.String(path)
	if err != nil {
		return err
	}
	namePtr, err := i.arena.String(name)
	if err != nil {
		return err
	}

	outPtr, err := i.result()
	if err != nil {
		return err
	}

	used := i.mod.Xdefine_module_function(
		pathPtr, int32(len(path)),
		namePtr, int32(len(name)),
		i.register(fn),
		outPtr, defaultValueArenaCapacity,
	)
	_, err = i.consumeArena(outPtr, used)
	return err
}

func (i *Module) setModuleAttr(path, name string, v value.Value) error {
	defer i.arena.Mark()()

	pathPtr, err := i.arena.String(path)
	if err != nil {
		return err
	}
	namePtr, err := i.arena.String(name)
	if err != nil {
		return err
	}

	valuePtr, err := i.arena.New(codec.ValueSize)
	if err != nil {
		return err
	}
	if err := i.codec.EncodeInto(i.arena, valuePtr, v); err != nil {
		return err
	}

	outPtr, err := i.result()
	if err != nil {
		return err
	}

	used := i.mod.Xset_module_attr(
		pathPtr, int32(len(path)),
		namePtr, int32(len(name)),
		valuePtr,
		outPtr, defaultValueArenaCapacity,
	)
	_, err = i.consumeArena(outPtr, used)
	return err
}
