package micropython

// func TestGC(t *testing.T) {
// 	instance := newT(t)

// 	instance.Exec(t.Context(), "doubler = lambda x: x * 2")

// 	val, _ := instance.Eval(t.Context(), "doubler")

// 	callable, _ := val.AsCallable()

// 	result, _ := callable(t.Context(), instance, 2)

// 	doubled, _ := result.AsInt()

// 	if doubled != 4 {
// 		t.Fatalf("should be 4, got %d", doubled)
// 	}

// 	val = Value{}
// 	callable = nil
// 	result = Value{}

// 	runtime.GC()

// 	_, err := instance.Eval(t.Context(), "doubler")
// 	if err == nil {
// 		t.Fatalf("double should've been GC'd")
// 	}

// }
