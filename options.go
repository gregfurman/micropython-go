package micropython

import (
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math"
	"net"
	"slices"

	"github.com/gregfurman/micropython-go/internal/host/env"
	"github.com/gregfurman/micropython-go/internal/host/network"
)

type options struct {
	maxIdle    int
	maxIdleSet bool
	heapBytes  int64

	globals      map[string]any
	hostFuncs    map[string]HostFunc
	sourceScript string
	stdout       io.Writer
	filesystem   fs.FS
	vars         map[string]string

	netGrants   []netGrant
	resolver    *net.Resolver
	resolverSet bool
}

type netGrant struct {
	transport network.Transport
	address   string
	port      int
}

// ProgramOption configures a Program. Every Option is also a ProgramOption.
// [WithMaxIdle] controls the pool; the other options also apply to instances.
type ProgramOption interface {
	apply(*options)
}

// Option configures an Instance or a Program.
// Access is opt-in: use [WithFS], [WithEnv], [WithTCPAccess], [WithUDPAccess],
// [WithDNSResolver], [WithStdout], and [WithHostFunc] to expose host resources.
// [WithSource], [WithGlobals], and [WithHeapSize] configure execution.
type Option interface {
	ProgramOption
	instanceOption()
}

type optionFunc func(*options)

func (f optionFunc) apply(o *options) { f(o) }
func (optionFunc) instanceOption()    {}

type programOptionFunc func(*options)

func (f programOptionFunc) apply(o *options) { f(o) }

// WithHeapSize sets the Python heap size in bytes. The default and zero use 128 KiB.
// This is not a limit on total interpreter or host memory.
// Invalid sizes fail at construction. Exhausting the heap raises MemoryError.
func WithHeapSize(bytes int) Option {
	return optionFunc(func(o *options) {
		o.heapBytes = int64(bytes)
	})
}

// WithMaxIdle limits idle interpreters retained by a Program, not active runs.
// The default and zero use runtime.NumCPU; negative values fail at construction.
func WithMaxIdle(n int) ProgramOption {
	return programOptionFunc(func(o *options) {
		o.maxIdle = n
		o.maxIdleSet = true
	})
}

// WithHostFunc binds fn to a Python global before source execution.
// No Go callbacks are registered by default. The callback controls what host
// resources Python can access through it.
// Repeated names use the last binding. Programs and clones share the Go closure,
// which must support concurrent calls; its state is not rewound. See [HostFunc].
func WithHostFunc(name string, fn HostFunc) Option {
	return optionFunc(func(o *options) {
		if o.hostFuncs == nil {
			o.hostFuncs = make(map[string]HostFunc, 1)
		}
		o.hostFuncs[name] = fn
	})
}

// WithSource runs src after binding globals and host functions at initialization.
// By default, no initialization script runs. Repeated use replaces the script.
// For a [Program], the resulting Python state is the starting point for each run.
func WithSource(src string) Option {
	return optionFunc(func(o *options) {
		o.sourceScript = src
	})
}

// Globals maps Python global names to initial Go values for [WithGlobals].
type Globals = map[string]any

// WithGlobals sets initial Python globals using [Instance.Set] conversion rules.
// No caller-supplied globals are set by default. Repeated use replaces the map.
func WithGlobals(g Globals) Option {
	return optionFunc(func(o *options) {
		o.globals = g
	})
}

func newOptions[T ProgramOption](opts []T) *options {
	o := &options{}
	for _, opt := range opts {
		opt.apply(o)
	}
	return o
}

// validate reports a configuration that cannot be honoured, rather than
// narrowing it silently. A heap size is an int on the way in and an int32 on
// the way down, so a 64-bit value has to be checked before it is cast.
func (o *options) validate() error {
	switch {
	case o.heapBytes < 0:
		return fmt.Errorf("micropython: heap size %d is negative", o.heapBytes)
	case o.heapBytes > math.MaxInt32:
		return fmt.Errorf("micropython: heap size %d exceeds the %d byte maximum", o.heapBytes, math.MaxInt32)
	case o.maxIdleSet && o.maxIdle < 0:
		return fmt.Errorf("micropython: idle interpreter count %d is negative", o.maxIdle)
	case o.resolverSet && o.resolver == nil:
		return fmt.Errorf("micropython: DNS resolver must not be nil")
	}

	for _, name := range slices.Sorted(maps.Keys(o.vars)) {
		if err := env.Validate(name, o.vars[name]); err != nil {
			return fmt.Errorf("micropython: environment variable: %w", err)
		}
	}

	if _, err := o.allowList(); err != nil {
		return err
	}
	return nil
}

// WithStdout sends Python's print output to w. By default, or with nil, it is discarded.
// Repeated use replaces the writer.
// The caller owns w. Programs and clones share it without synchronization,
// so it must support concurrent writes when used concurrently.
func WithStdout(w io.Writer) Option {
	return optionFunc(func(o *options) {
		o.stdout = w
	})
}

// WithFS exposes filesystem at Python's root for files and imports.
// Access is denied by default or with nil. Repeated use replaces the filesystem.
// Open files are closed on rewind or instance close, but the caller owns the
// filesystem. Programs and clones share it; filesystem changes are not rewound.
//
// The fs.FS interface provides read access. Backends can grant writes with
// [OpenFileFS], [MkdirFS], [UnlinkFS], [RmdirFS], and [RenameFS].
// Use [ReadOnly] to hide those write capabilities.
//
// The backend must confine symlinks and support concurrent use by independent
// instances. os.DirFS alone does not prevent symlinks escaping its directory.
func WithFS(filesystem fs.FS) Option {
	return optionFunc(func(o *options) { o.filesystem = filesystem })
}

// WithEnv sets a variable for Python's os.getenv. The environment starts empty.
// Calls are additive; the last value for a name wins.
//
// The Go process environment is never inherited. Python's os.putenv and
// os.unsetenv affect only this instance. Clones copy the current variables;
// Program.Run restores the variables captured after initialization.
//
// Names must be nonempty, at most 1024 bytes, and contain neither "=" nor NUL.
// Values must be at most 65536 bytes and contain no NUL. Invalid pairs fail
// construction. Guest writes use the same limits and cannot add a new name
// when 256 variables already exist. This host memory is outside WithHeapSize.
func WithEnv(name, value string) Option {
	return optionFunc(func(o *options) {
		if o.vars == nil {
			o.vars = make(map[string]string)
		}
		o.vars[name] = value
	})
}

// AnyAddress matches all supported destination addresses, including loopback
// and private networks.
const AnyAddress = network.AnyAddress

// WithTCPAccess permits outbound TCP to an IPv4 address, CIDR block, or
// [AnyAddress], on port 1-65535. Hostnames and IPv6 are not supported.
// Connections are denied by default. Grants are additive and order-independent.
//
//	WithTCPAccess("192.0.2.10", 443)
//	WithTCPAccess("10.0.0.0/8", 5432)
//	WithTCPAccess(AnyAddress, 443)
//
// DNS requires [WithDNSResolver]. Invalid grants fail at construction.
// This opens no sockets; port 443 permits TCP traffic, not just HTTPS.
func WithTCPAccess(address string, port int) Option {
	return optionFunc(func(o *options) {
		o.netGrants = append(o.netGrants, netGrant{transport: network.TCP, address: address, port: port})
	})
}

// WithUDPAccess permits outbound UDP on the same terms as [WithTCPAccess].
// Connections are denied by default. Repeated use adds grants.
// Python must connect the socket first. sendto may only name the connected
// peer; recvfrom returns that peer. Unconnected datagrams are unsupported.
func WithUDPAccess(address string, port int) Option {
	return optionFunc(func(o *options) {
		o.netGrants = append(o.netGrants, netGrant{transport: network.UDP, address: address, port: port})
	})
}

// WithDNSResolver supplies and enables name resolution for socket.getaddrinfo.
// Resolution is denied by default. Nil fails at construction; use
// net.DefaultResolver explicitly to use the host resolver. Last call wins.
//
//	WithTCPAccess(AnyAddress, 443)
//	WithDNSResolver(net.DefaultResolver)
//
// Lookups are independent of TCP/UDP grants and can contact nameservers outside
// those grants. Resolved destinations still need connection permission.
// Programs and clones share r; do not modify it while they are in use.
func WithDNSResolver(r *net.Resolver) Option {
	return optionFunc(func(o *options) { o.resolver, o.resolverSet = r, true })
}

func (o *options) allowList() (*network.AllowList, error) {
	list := &network.AllowList{}
	for _, g := range o.netGrants {
		if err := list.Add(g.transport, g.address, g.port); err != nil {
			return nil, fmt.Errorf("micropython: %w", err)
		}
		if err := network.ValidateIPv4Address(g.address); err != nil {
			return nil, fmt.Errorf("micropython: %w", err)
		}
	}
	return list, nil
}

// network folds the options into the host configuration. Connecting and
// resolving are granted separately, and neither defaults to on.
func (o *options) network() (network.Config, error) {
	allowed, err := o.allowList()
	if err != nil {
		return network.Config{}, err
	}

	conf := network.Config{DialContext: network.DenyAll}
	if len(o.netGrants) > 0 {
		conf.DialContext = allowed.Dial((&net.Dialer{}).DialContext)
	}

	if o.resolver != nil {
		conf.LookupIP = o.resolver.LookupIP
	}

	return conf, nil
}
