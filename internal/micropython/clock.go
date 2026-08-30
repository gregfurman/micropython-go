package micropython

import "time"

const (
	// delaySlice bounds how long a sleep can ignore Cancel. The guest cannot
	// reach its VM hook while it is blocked in a delay, so the delay polls.
	delaySlice = 5 * time.Millisecond
)

var epoch = time.Now()

func (m *Module) _mp_hal_ticks_ms() int32 {
	return int32(time.Since(epoch).Milliseconds())
}

func (m *Module) _mp_hal_ticks_us() int32 {
	return int32(time.Since(epoch).Microseconds())
}

// Xmp_hal_ticks_cpu has no cycle counter to read, so it reports microseconds.
// Ports without one return 0, which makes ticks_diff useless on it.
func (m *Module) _mp_hal_ticks_cpu() int32 {
	return m._mp_hal_ticks_us()
}

func (m *Module) _mp_hal_delay_ms(ms int32) {
	if ms <= 0 {
		return
	}

	deadline := time.Now().Add(time.Duration(ms) * time.Millisecond)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 || m._env.Xhost_poll() != 0 {
			return
		}
		time.Sleep(min(remaining, delaySlice))
	}
}

func (m *Module) _mp_hal_delay_us(us int32) {
	if us <= 0 {
		return
	}
	if d := time.Duration(us) * time.Microsecond; d < delaySlice {
		time.Sleep(d)
		return
	}

	m._mp_hal_delay_ms(us / 1000)
}
