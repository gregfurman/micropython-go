package micropython

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strings"
	"testing"
)

//go:embed micropython/tests/basics micropython/tests/float micropython/tests/stress
var pythonTestsFS embed.FS

//go:embed testdata/tests_basics_snapshot.json testdata/tests_float_snapshot.json testdata/tests_stress_snapshot.json
var snapshotsFS embed.FS

type snapshot struct {
	Recorded struct {
		Stdout string `json:"stdout"`
	} `json:"recorded"`
}

func TestSnapshot(t *testing.T) {
	entries, err := snapshotsFS.ReadDir("testdata")
	if err != nil {
		t.Fatalf("could not list snapshots: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_snapshot.json") {
			continue
		}

		suiteFile := path.Join("testdata", entry.Name())
		contents, err := snapshotsFS.ReadFile(suiteFile)
		if err != nil {
			t.Fatalf("could not read %s: %v", entry.Name(), err)
		}

		suite := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "tests_"), "_snapshot.json")
		t.Run(suite, func(t *testing.T) {
			runSuite(t, contents)
		})
	}
}

func runSuite(t *testing.T, contents []byte) {
	var snapshots map[string]snapshot
	if err := json.Unmarshal(contents, &snapshots); err != nil {
		t.Fatalf("could not load snapshots: %v", err)
	}

	paths := make([]string, 0, len(snapshots))
	for p := range snapshots {
		paths = append(paths, p)
	}
	// HACK: sort paths for deterministic ordering....
	slices.Sort(paths)

	for _, testPath := range paths {
		snap := snapshots[testPath]

		t.Run(testPath, func(t *testing.T) {
			if why := skipReason(testPath); why != "" {
				t.Skip(why)
			}
			t.Logf("running: %s", testPath)

			var out bytes.Buffer
			in, err := NewInstance(t.Context(), WithStdout(&out))
			if err != nil {
				t.Fatalf("could not create Instance: %v", err)
			}
			defer in.Close()

			src, err := pythonTestsFS.ReadFile(testPath)
			if err != nil {
				t.Fatalf("could not load script at [%s]: %v", testPath, err)
			}

			err = in.Exec(t.Context(), string(src))
			if err != nil {
				var errDetails string
				if exc, ok := errors.AsType[*PythonError](err); ok {
					errDetails = exc.Raw()
				} else {
					errDetails = err.Error()
				}
				t.Log(out.String())
				t.Errorf("Instance.Exec failed with exception:\n%s", errDetails)
				return
			}
			if want, got := snap.Recorded.Stdout, out.String(); want != got {
				t.Errorf("unexpected stdout:\nwant: %q\ngot:  %q", want, got)
			}
		})
	}
}

// ----------------------------------------------------------------------------

var skippedTests = [...]struct {
	name   string
	path   string
	reason string
}{
	{
		name:   "attrtuple2",
		path:   "micropython/tests/basics/attrtuple2.py",
		reason: "needs os, which has no meaning without a filesystem",
	},
	{
		name:   "builtin_help",
		path:   "micropython/tests/basics/builtin_help.py",
		reason: "help() lists the modules a build has, so the text is config-specific",
	},
	{
		name:   "bytearray_slice_assign",
		path:   "micropython/tests/basics/bytearray_slice_assign.py",
		reason: "needs bytearray slice assignment",
	},
	{
		name:   "class_setname_hazard_rand",
		path:   "micropython/tests/basics/class_setname_hazard_rand.py",
		reason: "needs random",
	},
	{
		name:   "fun_code_colines",
		path:   "micropython/tests/basics/fun_code_colines.py",
		reason: "needs __code__.co_lines, beyond MICROPY_PY_FUNCTION_ATTRS_CODE",
	},
	{
		name:   "fun_code_full",
		path:   "micropython/tests/basics/fun_code_full.py",
		reason: "needs the full __code__ attribute set",
	},
	{
		name:   "import_star_nonmodule",
		path:   "micropython/tests/basics/import_star_nonmodule.py",
		reason: "import * from a class honours __all__ differently here",
	},
	{
		name:   "io_iobase",
		path:   "micropython/tests/basics/io_iobase.py",
		reason: "subclassing io.IOBase raises TypeError: argument num/types mismatch",
	},
	{
		name:   "memoryview_slice_assign",
		path:   "micropython/tests/basics/memoryview_slice_assign.py",
		reason: "needs uctypes",
	},
	{
		name:   "memoryview_slice_size",
		path:   "micropython/tests/basics/memoryview_slice_size.py",
		reason: "needs uctypes",
	},
	{
		name:   "nanbox_smallint",
		path:   "micropython/tests/basics/nanbox_smallint.py",
		reason: "only meaningful in a nan-boxing build",
	},
	{
		name:   "string_module_tstring",
		path:   "micropython/tests/basics/string_module_tstring.py",
		reason: "import string.templatelib fails; only the attribute resolves",
	},
	{
		name:   "string_tstring_basic",
		path:   "micropython/tests/basics/string_tstring_basic.py",
		reason: "import string.templatelib fails; only the attribute resolves",
	},
	{
		name:   "string_tstring_constructor",
		path:   "micropython/tests/basics/string_tstring_constructor.py",
		reason: "import string.templatelib fails; only the attribute resolves",
	},
	{
		name:   "string_tstring_constructor1",
		path:   "micropython/tests/basics/string_tstring_constructor1.py",
		reason: "import string.templatelib fails; only the attribute resolves",
	},
	{
		name:   "string_tstring_errors1",
		path:   "micropython/tests/basics/string_tstring_errors1.py",
		reason: "import string.templatelib fails; only the attribute resolves",
	},
	{
		name:   "string_tstring_operations",
		path:   "micropython/tests/basics/string_tstring_operations.py",
		reason: "import string.templatelib fails; only the attribute resolves",
	},
	{
		name:   "string_tstring_parser1",
		path:   "micropython/tests/basics/string_tstring_parser1.py",
		reason: "import string.templatelib fails; only the attribute resolves",
	},
	{
		name:   "subclass_native_call",
		path:   "micropython/tests/basics/subclass_native_call.py",
		reason: "needs machine, a hardware binding",
	},
	{
		name:   "sys_path",
		path:   "micropython/tests/basics/sys_path.py",
		reason: "needs sys.path and __file__, which this build deliberately omits",
	},
	{
		name:   "sys_stdio",
		path:   "micropython/tests/basics/sys_stdio.py",
		reason: "needs print(file=...), which still raises TypeError with STDFILES on",
	},
	{
		name:   "sys_stdio_buffer",
		path:   "micropython/tests/basics/sys_stdio_buffer.py",
		reason: "needs print(file=...), which still raises TypeError with STDFILES on",
	},
	{
		name:   "sys_tracebacklimit",
		path:   "micropython/tests/basics/sys_tracebacklimit.py",
		reason: "the reference strips filenames from tracebacks; this build reports <string>",
	},
	{
		name:   "weakref_callback_exception",
		path:   "micropython/tests/basics/weakref_callback_exception.py",
		reason: "an exception in a weakref callback prints a traceback here rather than being swallowed",
	},
	{
		name:   "cmath_dunder",
		path:   "micropython/tests/float/cmath_dunder.py",
		reason: "needs cmath",
	},
	{
		name:   "cmath_fun",
		path:   "micropython/tests/float/cmath_fun.py",
		reason: "needs cmath",
	},
	{
		name:   "cmath_fun_special",
		path:   "micropython/tests/float/cmath_fun_special.py",
		reason: "needs cmath",
	},
	{
		name:   "float_format_accuracy",
		path:   "micropython/tests/float/float_format_accuracy.py",
		reason: "needs random",
	},
	{
		name:   "math_constants_extra",
		path:   "micropython/tests/float/math_constants_extra.py",
		reason: "needs MICROPY_PY_MATH_CONSTANTS",
	},
	{
		name:   "math_domain_special",
		path:   "micropython/tests/float/math_domain_special.py",
		reason: "needs MICROPY_PY_MATH_SPECIAL_FUNCTIONS",
	},
	{
		name:   "math_factorial_intbig",
		path:   "micropython/tests/float/math_factorial_intbig.py",
		reason: "needs MICROPY_PY_MATH_FACTORIAL",
	},
	{
		name:   "math_fun_special",
		path:   "micropython/tests/float/math_fun_special.py",
		reason: "needs MICROPY_PY_MATH_SPECIAL_FUNCTIONS",
	},
	{
		name:   "math_isclose",
		path:   "micropython/tests/float/math_isclose.py",
		reason: "needs MICROPY_PY_MATH_ISCLOSE",
	},
	{
		name:   "bytecode_limit",
		path:   "micropython/tests/stress/bytecode_limit.py",
		reason: "tunes itself on sys.implementation._mpy, which needs persistent code support",
	},
	{
		name:   "async_await",
		path:   "micropython/tests/basics/async_await.py",
		reason: "async/await is not compiled in",
	},
	{
		name:   "async_await2",
		path:   "micropython/tests/basics/async_await2.py",
		reason: "async/await is not compiled in",
	},
	{
		name:   "async_def",
		path:   "micropython/tests/basics/async_def.py",
		reason: "async/await is not compiled in",
	},
	{
		name:   "async_for",
		path:   "micropython/tests/basics/async_for.py",
		reason: "async/await is not compiled in",
	},
	{
		name:   "async_for2",
		path:   "micropython/tests/basics/async_for2.py",
		reason: "async/await is not compiled in",
	},
	{
		name:   "async_with",
		path:   "micropython/tests/basics/async_with.py",
		reason: "async/await is not compiled in",
	},
	{
		name:   "async_with2",
		path:   "micropython/tests/basics/async_with2.py",
		reason: "async/await is not compiled in",
	},
	{
		name:   "async_with_break",
		path:   "micropython/tests/basics/async_with_break.py",
		reason: "async/await is not compiled in",
	},
	{
		name:   "async_with_return",
		path:   "micropython/tests/basics/async_with_return.py",
		reason: "async/await is not compiled in",
	},
	{
		name:   "string_tstring_basic1",
		path:   "micropython/tests/basics/string_tstring_basic1.py",
		reason: "uses t\"\\8\", an escape MicroPython rejects and CPython only warns about",
	},
	{
		name:   "recursive_data",
		path:   "micropython/tests/stress/recursive_data.py",
		reason: "needs print(file=...), which still raises TypeError with STDFILES on",
	},
}

func skipReason(testPath string) string {
	for _, skip := range skippedTests {
		if skip.path == testPath {
			return skip.reason
		}
	}

	return ""
}
