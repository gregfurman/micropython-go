package api

import (
	"reflect"
	"testing"
)

type Row struct {
	ID   string   `json:"id"`
	A    int      `json:"a"`
	B    float64  `json:"b"`
	Tags []string `json:"tags"`
}

type Score struct {
	ID    string `json:"id"`
	Score int    `json:"score"`
	OK    bool   `json:"ok"`
}

func instance(t *testing.T) *Instance {
	t.Helper()
	in, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestStructRoundTrip(t *testing.T) {
	fn, err := instance(t).Define[Row, Score]("score", `
def score(row):
    total = int(row["a"] * 2 + row["b"])
    return {"id": row["id"], "score": total, "ok": total > 10}
`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fn(Row{ID: "r-1", A: 4, B: 5.5, Tags: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Score{ID: "r-1", Score: 13, OK: true}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestScalars(t *testing.T) {
	in := instance(t)

	double, err := in.Define[int, int]("double", "def double(n):\n    return n * 2\n")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := double(21); err != nil || got != 42 {
		t.Errorf("double(21) = %v, %v", got, err)
	}

	upper, err := in.Define[string, string]("upper", "def upper(s):\n    return s.upper()\n")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := upper("abc"); err != nil || got != "ABC" {
		t.Errorf("upper = %q, %v", got, err)
	}

	half, err := in.Define[int, float64]("half", "def half(n):\n    return n / 2\n")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := half(5); err != nil || got != 2.5 {
		t.Errorf("half(5) = %v, %v", got, err)
	}
}

func TestSliceAndMap(t *testing.T) {
	in := instance(t)

	cumsum, err := in.Define[[]int, []int]("cumsum", `
def cumsum(xs):
    out, total = [], 0
    for x in xs:
        total += x
        out.append(total)
    return out
`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := cumsum([]int{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int{1, 3, 6}) {
		t.Errorf("cumsum = %v", got)
	}

	counts, err := in.Define[[]string, map[string]int]("counts", `
def counts(words):
    out = {}
    for w in words:
        out[w] = out.get(w, 0) + 1
    return out
`)
	if err != nil {
		t.Fatal(err)
	}
	m, err := counts([]string{"a", "b", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, map[string]int{"a": 2, "b": 1}) {
		t.Errorf("counts = %v", m)
	}
}

func TestVarFunc(t *testing.T) {
	in := instance(t)
	if _, err := in.Exec("def add(a, b, c):\n    return a + b + c\n"); err != nil {
		t.Fatal(err)
	}
	add, err := in.BindVar[int]("add")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := add(1, 2, 3); err != nil || got != 6 {
		t.Errorf("add = %v, %v", got, err)
	}
}

func TestCallableAndAny(t *testing.T) {
	fn, err := instance(t).Define[any, any]("echo", "def echo(v):\n    return v\n")
	if err != nil {
		t.Fatal(err)
	}
	got, err := fn(map[string]any{"k": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]any{"k": int64(1)}) {
		t.Errorf("echo = %#v", got)
	}
}

func TestErrors(t *testing.T) {
	in := instance(t)

	boom, err := in.Define[int, int]("boom", "def boom(n):\n    raise ValueError('nope')\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := boom(1); err == nil {
		t.Fatal("expected a Python error")
	}

	bad, err := in.Define[int, string]("bad", "def bad(n):\n    return n\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad(1); err == nil {
		t.Fatal("expected a decode error")
	}
}
