package main

func Example_main() {
	main()
	// Output:
	// Of(1) -> int
	// Of("hello") -> str
	// Of([]byte{0x1, 0x2}) -> bytes
	// Of([]interface {}{1, 2}) -> list
	// Of(map[string]interface {}{"a":1}) -> dict
	// Of(struct { A int "json:\"a\"" }{A:1}) -> dict
	// built -> list
	// built -> tuple
	// built -> set
	// built -> frozenset
	// built -> dict
	// built -> NoneType
	// export: map[id:s-1 tags:[cold] weights:[1.5 2.5]]
	//   id: s-1 (str)
	//   tags: [cold] (set)
	//   weights: [1.5 2.5] (tuple)
}
