package host

import (
	"testing"
)

// The C decoder in build/wasm_build.c is a hand-written parser with manual
// bounds checks, reading a buffer it does not produce. Feeding it arbitrary
// bytes is the sharpest thing to fuzz here: a missing check is an out-of-bounds
// read inside the module, and the failure mode is a trap that surfaces as a Go
// panic through wasm2go.
//
// The invariant is not that garbage decodes -- it is that garbage is *rejected*
// without taking the interpreter with it.
func FuzzDecodeArgs(f *testing.F) {
	f.Add([]byte{}, uint8(0))
	f.Add([]byte{0}, uint8(1))                                    // none
	f.Add([]byte{3, 1, 0, 0, 0, 0, 0, 0, 0}, uint8(1))            // int 1
	f.Add([]byte{5, 2, 0, 0, 0, 'h', 'i'}, uint8(1))              // str "hi"
	f.Add([]byte{7, 1, 0, 0, 0, 2}, uint8(1))                     // list of 1 true
	f.Add([]byte{9, 1, 0, 0, 0, 5, 1, 0, 0, 0, 'k', 0}, uint8(1)) // dict {"k": None}
	f.Add([]byte{5, 255, 255, 255, 255}, uint8(1))                // str claiming 4GB
	f.Add([]byte{7, 255, 255, 255, 255}, uint8(1))                // list claiming 4G items
	f.Add([]byte{9, 255, 255, 255, 255}, uint8(1))                // dict claiming 4G pairs
	f.Add([]byte{99}, uint8(1))                                   // unknown tag

	a := New()
	if err := a.Eval("def echo(*a):\n    return a\n", ModeExec); err != nil {
		f.Fatal(err)
	}
	handle, err := a.Func("echo")
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, buf []byte, nargs uint8) {
		ptr, err := a.Write(buf)
		if err != nil {
			t.Skip()
		}

		// Whatever this does, it must come back as a value or an error.
		_ = a.check(a.mod.Xmp_api_call(handle, ptr, int32(len(buf)), int32(nargs%16)))

		// And the interpreter has to survive it.
		if err := a.Eval("1 + 1", ModeValue); err != nil {
			t.Fatalf("interpreter broken after decoding %x (nargs=%d): %v", buf, nargs%16, err)
		}
		got, err := a.Value()
		if err != nil {
			t.Fatalf("interpreter broken after decoding %x: %v", buf, err)
		}
		if got != int64(2) {
			t.Fatalf("interpreter wrong after decoding %x: 1+1 = %#v", buf, got)
		}
	})
}
