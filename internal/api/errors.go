package api

import (
	"errors"
	"fmt"

	"github.com/gregfurman/micropython-go/internal/value"
)

var (
	ErrClosed      = errors.New("micropython: instance is closed")
	ErrInterrupted = value.ErrInterrupted
)

// TrapError reports a failure in the generated Wasm machine rather than a
// Python exception. A trapped instance remains unusable until reset/restored.
type TrapError struct {
	Value any
	Stack []byte
}

func (e *TrapError) Error() string { return fmt.Sprintf("micropython: trap: %v", e.Value) }
