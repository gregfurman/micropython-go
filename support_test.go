package micropython

// TODO: This should be kept in sync with SUPPORT_MATRIX.md

import (
	"context"
	"errors"
	"testing"
)

type state int

const (
	supported   state = iota // works in micropython-go
	missingHere              // absent here, present in upstream MicroPython
	missingBoth              // absent from both; not something this build dropped
)

func (s state) String() string {
	switch s {
	case supported:
		return "supported"
	case missingHere:
		return "missing here (upstream has it)"
	default:
		return "missing in both"
	}
}

func (s state) wantAvailable() bool { return s == supported }

func check(t *testing.T, subject string, want state, run func() error) {
	t.Helper()
	err := run()
	got := err == nil
	if got == want.wantAvailable() {
		return
	}
	if got {
		t.Errorf("%s: works, but SUPPORT_MATRIX.md records it as %s", subject, want)
		return
	}
	t.Errorf("%s: SUPPORT_MATRIX.md records it as %s, but it failed: %v", subject, want, err)
}

func TestSupportedModules(t *testing.T) {
	ctx := context.Background()
	in := newT(t)

	modules := []struct {
		name string
		want state
	}{
		{"array", supported}, {"collections", supported}, {"gc", supported},
		{"io", supported}, {"json", supported}, {"math", supported},
		{"micropython", supported}, {"re", supported}, {"struct", supported},
		{"sys", supported}, {"time", supported},

		{"asyncio", missingHere}, {"binascii", missingHere}, {"btree", missingHere},
		{"cmath", missingHere}, {"deflate", missingHere}, {"errno", missingHere},
		{"hashlib", missingHere}, {"heapq", missingHere}, {"machine", missingHere},
		{"os", missingHere}, {"platform", missingHere}, {"random", missingHere},
		{"select", missingHere}, {"socket", missingHere}, {"ssl", missingHere},
		{"termios", missingHere}, {"uctypes", missingHere},
		{"vfs", missingHere}, {"_thread", missingHere},

		{"abc", missingBoth}, {"base64", missingBoth}, {"copy", missingBoth},
		{"dataclasses", missingBoth}, {"datetime", missingBoth}, {"decimal", missingBoth},
		{"enum", missingBoth}, {"functools", missingBoth}, {"inspect", missingBoth},
		{"itertools", missingBoth}, {"logging", missingBoth}, {"operator", missingBoth},
		{"pickle", missingBoth}, {"threading", missingBoth}, {"traceback", missingBoth},
		{"typing", missingBoth}, {"unittest", missingBoth}, {"warnings", missingBoth},
		{"weakref", missingBoth},

		{"importlib", missingBoth}, // VERIFY THIS
	}

	for _, m := range modules {
		check(t, "import "+m.name, m.want, func() error {
			_, err := in.Exec(ctx, "import "+m.name)
			return err
		})
	}
}

func TestSupportedTemplatelib(t *testing.T) {
	ctx := context.Background()
	in := newT(t)

	got, err := in.Eval(ctx, "sorted(dir(__import__('string')))")
	if err != nil {
		t.Fatalf("import string: %v", err)
	}
	names, ok := got.([]any)
	if !ok {
		t.Fatalf("dir(string) = %#v, want a list", got)
	}
	var found bool
	for _, n := range names {
		if n == "templatelib" {
			found = true
		}
	}
	if !found {
		t.Errorf("dir(string) = %v, want it to contain templatelib", names)
	}
	// It is a t-strings artifact, not the CPython string module.
	if _, err := in.Eval(ctx, "__import__('string').ascii_lowercase"); err == nil {
		t.Error("string.ascii_lowercase resolved; SUPPORT_MATRIX.md says this is not the full module")
	}
}

func TestSupportedBuiltins(t *testing.T) {
	ctx := context.Background()
	in := newT(t)

	builtins := []struct {
		name string
		want state
	}{
		{"property", missingHere}, {"min", missingHere}, {"max", missingHere},
		{"filter", missingHere}, {"enumerate", missingHere}, {"input", missingHere},
		{"NotImplemented", missingHere},

		{"format", missingBoth}, {"vars", missingBoth},

		{"abs", supported}, {"all", supported}, {"any", supported}, {"bin", supported},
		{"bool", supported}, {"bytearray", supported}, {"bytes", supported},
		{"callable", supported}, {"chr", supported}, {"classmethod", supported},
		{"compile", supported}, {"complex", supported}, {"delattr", supported},
		{"dict", supported}, {"dir", supported}, {"divmod", supported}, {"eval", supported},
		{"exec", supported}, {"float", supported}, {"frozenset", supported},
		{"getattr", supported}, {"globals", supported}, {"hasattr", supported},
		{"hash", supported}, {"hex", supported}, {"id", supported}, {"int", supported},
		{"isinstance", supported}, {"issubclass", supported}, {"iter", supported},
		{"len", supported}, {"list", supported}, {"locals", supported}, {"map", supported},
		{"memoryview", supported}, {"next", supported}, {"object", supported},
		{"oct", supported}, {"open", supported}, {"ord", supported}, {"pow", supported},
		{"print", supported}, {"range", supported}, {"repr", supported},
		{"reversed", supported}, {"round", supported}, {"set", supported},
		{"setattr", supported}, {"slice", supported}, {"sorted", supported},
		{"staticmethod", supported}, {"str", supported}, {"sum", supported},
		{"super", supported}, {"tuple", supported}, {"type", supported}, {"zip", supported},
	}

	for _, b := range builtins {
		check(t, b.name, b.want, func() error {
			_, err := in.Eval(ctx, b.name)
			return err
		})
	}
}

// TestBuiltinsPresentButUnusable covers the two names the matrix flags: they resolve, so a
// dir(builtins) listing looks complete, but calling them does not work.
func TestBuiltinsPresentButUnusable(t *testing.T) {
	ctx := context.Background()
	in := newT(t)

	if _, err := in.Eval(ctx, `open("x")`); err == nil {
		t.Error("open() succeeded; the matrix says it always raises OSError")
	} else if exc, ok := asPythonError(err); ok && exc.Type() != "OSError" {
		t.Errorf("open() raised %s, want OSError", exc.Type())
	}

	if _, err := in.Eval(ctx, "slice(1, 3)"); err == nil {
		t.Error("slice() constructed; the matrix says it cannot be in either build")
	}
	// Slice syntax is unaffected.
	if got, err := in.Eval(ctx, `"abcdef"[1:3]`); err != nil || got != "bc" {
		t.Errorf(`"abcdef"[1:3] = %#v, %v; want "bc"`, got, err)
	}
}

func TestSupportedLanguageFeatures(t *testing.T) {
	ctx := context.Background()

	features := []struct {
		name string
		src  string
		want state
	}{
		{"async def / await", "async def f(): return 1", missingHere},
		{"t-strings", "r = t'{1}'", supported},
		{"match / case", "x = 1\nmatch x:\n    case 1:\n        r = 1", missingBoth},

		{"f-strings", "r = f'{1+1}'", supported},
		{"f-string self-doc", "r = f'{1+1=}'", supported},
		{"walrus", "r = [y := 2]", supported},
		{"list comprehension", "r = [x for x in range(3)]", supported},
		{"dict comprehension", "r = {x: x for x in range(2)}", supported},
		{"set comprehension", "r = {x for x in range(2)}", supported},
		{"generator expression", "r = list(x for x in range(2))", supported},
		{"decorators", "def d(f):\n    return f\n@d\ndef g(): return 1\nr = g()", supported},
		{"yield", "def g():\n    yield 1\nr = list(g())", supported},
		{"yield from", "def a():\n    yield 1\ndef b():\n    yield from a()\nr = list(b())", supported},
		{"generator.send", "def g():\n    x = yield 1\n    yield x\nit = g()\nnext(it)\nr = it.send(5)", supported},
		{"classes", "class A:\n    def f(self): return 1\nr = A().f()", supported},
		{"multiple inheritance", "class A: pass\nclass B: pass\nclass C(A, B): pass\nr = C()", supported},
		{"super()", "class A:\n    def f(self): return 1\nclass B(A):\n    def f(self): return super().f()\nr = B().f()", supported},
		{"__slots__", "class A:\n    __slots__ = ('x',)\nr = A()", supported},
		{"classmethod", "class A:\n    @classmethod\n    def f(cls): return 1\nr = A.f()", supported},
		{"staticmethod", "class A:\n    @staticmethod\n    def f(): return 1\nr = A.f()", supported},
		{"property", "class A:\n    @property\n    def x(self): return 1\nr = A().x", missingHere},
		{"special methods", "class A:\n    def __add__(self, o): return 7\nr = A() + 1", supported},
		{"reverse special methods", "class A:\n    def __radd__(self, o): return 7\nr = 1 + A()", supported},
		{"__getattr__", "class A:\n    def __getattr__(self, n): return 9\nr = A().zz", supported},
		{"__call__", "class A:\n    def __call__(self): return 3\nr = A()()", supported},
		{"context managers", "class A:\n    def __enter__(self): return 1\n    def __exit__(self, *a): return False\nwith A() as v:\n    r = v", supported},
		{"try/except/else/finally", "try:\n    pass\nexcept Exception:\n    pass\nelse:\n    r = 1\nfinally:\n    pass", supported},
		{"raise from", "try:\n    try:\n        raise ValueError('a')\n    except ValueError as e:\n        raise TypeError('b') from e\nexcept TypeError:\n    r = 1", supported},
		{"custom exceptions", "class E(Exception): pass\ntry:\n    raise E('x')\nexcept E as e:\n    r = str(e)", supported},
		{"namedtuple", "from collections import namedtuple\nP = namedtuple('P', ('a', 'b'))\nr = P(1, 2).a", supported},
		{"keyword-only args", "def f(a, *, b=1): return b\nr = f(1, b=2)", supported},
		{"kwargs", "def f(**kw): return kw\nr = f(a=1)", supported},
		{"call unpacking", "def f(a, b): return a + b\nr = f(*[1], **{'b': 2})", supported},
		{"star unpack assignment", "a, *b = [1, 2, 3]\nr = b", supported},
		{"global / nonlocal", "def f():\n    def g():\n        nonlocal x\n        x = 2\n    x = 1\n    g()\n    return x\nr = f()", supported},
		{"assert", "assert True\nr = 1", supported},
		{"del", "d = {'a': 1}\ndel d['a']\nr = d", supported},
		{"function annotations", "def f(a: int) -> int: return a\nr = f(1)", supported},
		{"variable annotations", "x: int = 1\nr = x", supported},
		{"chained comparison", "r = 1 < 2 < 3", supported},
		{"conditional expression", "r = 1 if True else 2", supported},
		{"lambda", "r = (lambda a, b=1, *c, **d: a)(1)", supported},
	}

	for _, f := range features {
		in := newT(t)
		check(t, f.name, f.want, func() error {
			_, err := in.Exec(ctx, f.src)
			return err
		})
	}
}

func TestSupportedNumerics(t *testing.T) {
	ctx := context.Background()
	in := newT(t)

	// Arbitrary precision, so a value far past the small-int boundary is still exact.
	const big = "1606938044258990275541962092341162602522202993782792835301376"
	if got, err := in.Eval(ctx, "2**200 == "+big); err != nil || got != true {
		t.Errorf("2**200 exact = %#v, %v", got, err)
	}
	if got, err := in.Eval(ctx, "repr(0.1 + 0.2)"); err != nil || got != "0.30000000000000004" {
		t.Errorf("float is not a 64-bit double: %#v, %v", got, err)
	}
	if got, err := in.Eval(ctx, "(1+2j).imag"); err != nil || got != 2.0 {
		t.Errorf("complex = %#v, %v", got, err)
	}

	// wasm32, so the small-int boundary is 32-bit. The matrix records this exact value.
	if got, err := in.Eval(ctx, "__import__('sys').maxsize"); err != nil || got != int64(2147483647) {
		t.Errorf("sys.maxsize = %#v, %v; want 2147483647", got, err)
	}
	// It is a boundary, not a ceiling: larger values promote rather than overflow.
	if got, err := in.Eval(ctx, "__import__('sys').maxsize + 1 > 0"); err != nil || got != true {
		t.Errorf("maxsize+1 did not promote: %#v, %v", got, err)
	}
}

func TestSupportedStringAndBytes(t *testing.T) {
	ctx := context.Background()
	in := newT(t)

	cases := []struct {
		name string
		src  string
		want state
	}{
		{"bytes.hex()", `b"ab".hex()`, missingHere},
		{"str.center", `"a".center(3)`, missingHere},
		{"str.rjust", `"a".rjust(3)`, missingHere},
		{"str.format", `"{}-{}".format(1, 2)`, supported},
		{"% operator", `"%s-%d" % ("a", 1)`, supported},
		{"encode/decode", `"a".encode().decode()`, supported},
		{"memoryview slice", `bytes(memoryview(b"ab")[1:])`, supported},
		{"set algebra", `{1, 2} | {3}`, supported},
		{"frozenset", `frozenset([1, 2])`, supported},
		{"dict.fromkeys", `dict.fromkeys("ab")`, supported},
		{"range attrs", `range(1, 9, 2).step`, supported},
	}

	for _, c := range cases {
		check(t, c.name, c.want, func() error {
			_, err := in.Eval(ctx, c.src)
			return err
		})
	}
}

func asPythonError(err error) (*PythonError, bool) {
	var exc *PythonError
	return exc, errors.As(err, &exc)
}
