package host

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gregfurman/micropython-go/internal/value"
)

// The C and Go sides share a numbered table, and nothing but a comment has
// been keeping it in agreement. A mismatch does not fail: it decodes one kind
// as another, or calls the wrong reference operation, and shows up much later
// as a wrong value. This reads the headers and checks.

func TestABIConstantsMatchC(t *testing.T) {
	tables := []struct {
		header string
		prefix string
		want   map[string]int
	}{
		{"wasm_pack.h", "PK_", map[string]int{
			"PK_NONE": value.TagNone, "PK_FALSE": value.TagFalse, "PK_TRUE": value.TagTrue,
			"PK_INT": value.TagInt, "PK_FLOAT": value.TagFloat, "PK_STR": value.TagStr,
			"PK_BYTES": value.TagBytes, "PK_LIST": value.TagList, "PK_TUPLE": value.TagTuple,
			"PK_DICT": value.TagDict, "PK_SET": value.TagSet, "PK_FROZENSET": value.TagFrozenSet,
			"PK_EXCEPTION": value.TagException,
		}},
	}

	for _, table := range tables {
		t.Run(table.prefix, func(t *testing.T) {
			got, err := constants(table.header, table.prefix)
			if err != nil {
				t.Fatal(err)
			}

			// Every C constant must be answered for, so a new one added to the
			// header fails here rather than going unnoticed on this side.
			for name, c := range got {
				g, ok := table.want[name]
				if !ok {
					t.Errorf("%s is in %s but has no Go counterpart", name, table.header)
					continue
				}
				if g != c {
					t.Errorf("%s is %d in C, %d in Go", name, c, g)
				}
			}
			for name := range table.want {
				if _, ok := got[name]; !ok {
					t.Errorf("Go has %s but %s does not", name, table.header)
				}
			}
		})
	}
}

var constantRE = regexp.MustCompile(`^\s*(\w+)\s*=\s*(\d+)\s*,`)

func constants(header, prefix string) (map[string]int, error) {
	src, err := os.ReadFile("../../build/" + header)
	if err != nil {
		return nil, err
	}

	out := map[string]int{}
	for line := range strings.SplitSeq(string(src), "\n") {
		m := constantRE.FindStringSubmatch(line)
		if m == nil || !strings.HasPrefix(m[1], prefix) {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil {
			return nil, err
		}
		out[m[1]] = n
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no %s constants found in %s", prefix, header)
	}
	return out, nil
}
