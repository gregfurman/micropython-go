package micropython

import (
	"context"
	"fmt"
	"unicode"

	"github.com/gregfurman/micropython-go/internal/host"
	"github.com/gregfurman/micropython-go/internal/value"
)

// PackageSpec describes a Python package supplied by the embedding host.
// Package builds a specification; install it with Instance.InstallPackage or
// configure it before source execution with WithPackage.
type PackageSpec struct {
	name    string
	members []PackageMember
}

func (PackageSpec) packageMember() {}

// PackageMember is a function, value, or nested Package in a PackageSpec.
// The interface is sealed so future member kinds can preserve installation
// semantics without exposing the guest module implementation.
type PackageMember interface{ packageMember() }

type packageFunction struct {
	name string
	fn   HostFunc
}

func (packageFunction) packageMember() {}

type packageAttribute struct {
	name  string
	value Value
}

func (packageAttribute) packageMember() {}

// Package builds a Python package. Nested Package calls produce ordinary
// importable subpackages.
func Package(name string, members ...PackageMember) PackageSpec {
	return PackageSpec{name: name, members: members}
}

// Function adds a Go callback to a Package. Python receives a normal callable
// attribute backed by the interpreter's host-function registry.
func Function(name string, fn HostFunc) PackageMember {
	return packageFunction{name: name, fn: fn}
}

// Attribute adds a detached Python value to a Package. Values have the same
// conversion rules as Instance.Set and cannot be guest handles from another
// interpreter.
func Attribute(name string, v Value) PackageMember {
	return packageAttribute{name: name, value: v}
}

// RegisterPackage makes pkg available to ordinary Python imports in this
// interpreter. For example, registering Package("host", Function("log", fn))
// makes `import host; host.log(...)` available to later guest code.
func (i *Instance) RegisterPackage(ctx context.Context, pkg PackageSpec) error {
	if i.wrapped == nil {
		return ErrInstanceNotInitialised
	}

	internal, err := packageSpec(pkg)
	if err != nil {
		return err
	}
	return i.wrapped.InstallPackage(ctx, internal)
}

func packageSpec(spec PackageSpec) (host.Package, error) {
	if err := validPackageName(spec.name); err != nil {
		return host.Package{}, err
	}

	pkg := host.Package{
		Name:      spec.name,
		Functions: make(map[string]host.HostFunc),
		Values:    make(map[string]value.Value),
	}
	seen := make(map[string]struct{}, len(spec.members))
	for _, member := range spec.members {
		switch m := member.(type) {
		case packageFunction:
			if err := addPackageMember(seen, m.name); err != nil {
				return host.Package{}, err
			}
			if m.fn == nil {
				return host.Package{}, fmt.Errorf("package %q function %q: nil host function", spec.name, m.name)
			}
			pkg.Functions[m.name] = hostFunc(m.fn)
		case packageAttribute:
			if err := addPackageMember(seen, m.name); err != nil {
				return host.Package{}, err
			}
			pkg.Values[m.name] = m.value.val
		case PackageSpec:
			if err := addPackageMember(seen, m.name); err != nil {
				return host.Package{}, err
			}
			child, err := packageSpec(m)
			if err != nil {
				return host.Package{}, err
			}
			pkg.Packages = append(pkg.Packages, child)
		default:
			return host.Package{}, fmt.Errorf("package %q: unsupported member %T", spec.name, member)
		}
	}
	return pkg, nil
}

func addPackageMember(seen map[string]struct{}, name string) error {
	if err := validPackageName(name); err != nil {
		return err
	}
	if _, exists := seen[name]; exists {
		return fmt.Errorf("package member %q is defined more than once", name)
	}
	seen[name] = struct{}{}
	return nil
}

func validPackageName(name string) error {
	if name == "" {
		return fmt.Errorf("package names must not be empty")
	}
	for n, r := range name {
		if r == '_' || unicode.IsLetter(r) || (n > 0 && unicode.IsDigit(r)) {
			continue
		}
		return fmt.Errorf("%q is not a valid Python package name", name)
	}
	if pythonKeywords[name] {
		return fmt.Errorf("%q is a Python keyword", name)
	}
	return nil
}

var pythonKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true,
	"assert": true, "async": true, "await": true, "break": true, "class": true,
	"continue": true, "def": true, "del": true, "elif": true, "else": true,
	"except": true, "finally": true, "for": true, "from": true, "global": true,
	"if": true, "import": true, "in": true, "is": true, "lambda": true,
	"nonlocal": true, "not": true, "or": true, "pass": true, "raise": true,
	"return": true, "try": true, "while": true, "with": true, "yield": true,
}
