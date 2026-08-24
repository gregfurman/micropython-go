package micropython

import (
	"embed"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/gregfurman/micropython-go/internal/value"
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

			in, err := NewInstance(t.Context())
			if err != nil {
				t.Fatalf("could not create Instance: %v", err)
			}
			defer in.Close()

			src, err := pythonTestsFS.ReadFile(testPath)
			if err != nil {
				t.Fatalf("could not load script at [%s]: %v", testPath, err)
			}

			resp, err := in.Exec(t.Context(), string(src))
			if err != nil {
				var exc *value.Exception
				if errors.As(err, &exc) {
					switch exc.Type() {
					case "SystemExit":
						t.Skip("Skipped in MicroPython test suite.")
					case "NotImplementedError":
						t.Skipf("Skipping unimplemented functionality: %s", exc.Raw())
					case "ImportError":
						t.Skipf("Skipping test needing external import: %s", exc.Raw())
					}
				}

				t.Fatalf("Instance.Exec failed: %v", err)
			}

			got, _ := resp.(string)
			if want := snap.Recorded.Stdout; want != got {
				t.Errorf("unexpected stdout:\nwant: %q\ngot:  %q", want, got)
			}
		})
	}
}

func skipReason(testPath string) string {
	name := path.Base(testPath)

	if strings.HasPrefix(name, "async_") {
		return "async/await is not compiled in"
	}

	switch name {
	case "sys1.py":
		return "needs sys.path and sys.argv, which this build deliberately omits"

	case "string_tstring_basic1.py":
		return `uses t"\8", an escape MicroPython rejects and CPython only warns about`

	case "bytes_compare3.py":
		return "expected output is a ######## wildcard, not a literal"

	case "recursive_data.py":
		return "needs print(file=...), which would move print off the host's output hook"
	}

	return ""
}
