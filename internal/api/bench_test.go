package api

import "testing"

func benchInstance(b *testing.B) *Instance {
	b.Helper()
	in, err := New()
	if err != nil {
		b.Fatal(err)
	}
	if _, err := in.Exec("def add(a, b):\n    return a + b\n"); err != nil {
		b.Fatal(err)
	}
	if _, err := in.Exec("def echo(v):\n    return v\n"); err != nil {
		b.Fatal(err)
	}
	return in
}

func BenchmarkCallInts(b *testing.B) {
	in := benchInstance(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := in.Call("add", 20, 22); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCallMap(b *testing.B) {
	in := benchInstance(b)
	arg := map[string]any{"name": "widget", "qty": 3, "tags": []any{"a", "b"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := in.Call("echo", arg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFuncInts(b *testing.B) {
	in := benchInstance(b)
	fn, err := in.Func("add")
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := fn.Call(20, 22); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFuncMap(b *testing.B) {
	in := benchInstance(b)
	fn, err := in.Func("echo")
	if err != nil {
		b.Fatal(err)
	}
	arg := map[string]any{"name": "widget", "qty": 3, "tags": []any{"a", "b"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := fn.Call(arg); err != nil {
			b.Fatal(err)
		}
	}
}

// A realistic shape for the stateless-function case: a row in, a row out.
func BenchmarkFuncRealistic(b *testing.B) {
	in, err := New()
	if err != nil {
		b.Fatal(err)
	}
	fn, err := in.Define[map[string]any, any]("score", `
def score(row):
    total = row["a"] * 2 + row["b"]
    return {"id": row["id"], "score": total, "ok": total > 10}
`)
	if err != nil {
		b.Fatal(err)
	}
	row := map[string]any{"id": "r-1", "a": 4, "b": 5}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := fn(row); err != nil {
			b.Fatal(err)
		}
	}
}

// Splits the cost of a call into its parts, for tuning.

func splitInstance(b *testing.B) *Instance {
	in, err := New()
	if err != nil {
		b.Fatal(err)
	}
	_, err = in.Exec(`
def noop():
    return None
def sink(v):
    return None
M = {"name": "widget", "qty": 3, "tags": ["a", "b"]}
def src():
    return M
`)
	if err != nil {
		b.Fatal(err)
	}
	return in
}

var mapArg = map[string]any{"name": "widget", "qty": 3, "tags": []any{"a", "b"}}

func BenchmarkSplitNoop(b *testing.B) {
	in := splitInstance(b)
	fn, _ := in.Func("noop")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fn.Call()
	}
}

func BenchmarkSplitInbound(b *testing.B) {
	in := splitInstance(b)
	fn, _ := in.Func("sink")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fn.Call(mapArg)
	}
}

func BenchmarkSplitOutbound(b *testing.B) {
	in := splitInstance(b)
	fn, _ := in.Func("src")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fn.Call()
	}
}
