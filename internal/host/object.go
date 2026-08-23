package host

import "fmt"

// Call invokes a guest object the host is holding by reference.
//
// This is what a reference is for: a Python callable handed to a host function
// -- a lambda, a bound method -- and called back from it. The reference lives
// as long as the call that produced it, so one that arrives as an argument is
// live for the duration of that function.
func (o Object) Call(args ...any) (any, error) {
	if o.abi == nil {
		return nil, fmt.Errorf("micropython: %s is not a live reference", o.Type)
	}
	if !o.callable {
		return nil, fmt.Errorf("micropython: %s is not callable", o.Type)
	}
	return o.abi.callRef(o.abi, o.ref, o.epoch, args)
}
