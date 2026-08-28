package host

import "time"

const (
	// ticksMask keeps the counters inside a non-negative int32. The guest masks
	// again with MICROPY_PY_TIME_TICKS_PERIOD-1, a power of two that divides
	// this one, so wrapping here is invisible to it.
	ticksMask = 0x7fffffff

	// delaySlice bounds how long a sleep can ignore Cancel. The guest cannot
	// reach its VM hook while it is blocked in a delay, so the delay polls.
	delaySlice = 5 * time.Millisecond
)

func (e *Env) Xmp_hal_ticks_ms() int32 {
	return int32(time.Since(e.epoch).Milliseconds() & ticksMask)
}

func (e *Env) Xmp_hal_ticks_us() int32 {
	return int32(time.Since(e.epoch).Microseconds() & ticksMask)
}

// Xmp_hal_ticks_cpu has no cycle counter to read, so it reports microseconds.
// Ports without one return 0, which makes ticks_diff useless on it.
func (e *Env) Xmp_hal_ticks_cpu() int32 {
	return e.Xmp_hal_ticks_us()
}

func (e *Env) Xmp_hal_delay_ms(ms int32) {
	if ms <= 0 {
		return
	}

	deadline := time.Now().Add(time.Duration(ms) * time.Millisecond)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 || e.host.Poll() != 0 {
			return
		}
		time.Sleep(min(remaining, delaySlice))
	}
}

func (e *Env) Xmp_hal_delay_us(us int32) {
	if us <= 0 {
		return
	}
	if d := time.Duration(us) * time.Microsecond; d < delaySlice {
		time.Sleep(d)
		return
	}
	e.Xmp_hal_delay_ms(us / 1000)
}
