#ifndef HOST_H
#define HOST_H

#include <stdint.h>

// Application callbacks and reference bookkeeping imported from the host.

__attribute__((import_module("env"), import_name("host_trampoline"))) extern void host_trampoline(
    uint32_t func_id, uint32_t args_ptr, uint32_t args_size, uint32_t out_ptr, uint32_t out_capacity);

__attribute__((import_module("env"), import_name("host_stdout"))) extern void host_stdout(uint32_t ptr, uint32_t len);

__attribute__((import_module("env"), import_name("host_poll"))) extern int32_t host_poll(void);

// Socket imports return a descriptor/count/status on success, or -MP_Exxx.
// Descriptors must fit mod_network_socket_obj_t.fileno: 0..32767. They are
// host-owned tokens, not OS file descriptors. Status-only calls return zero.
// All pointers are borrowed wasm32 offsets, valid only during the import.
// IPv4 addresses occupy four bytes in network order; ports are scalar integers.
__attribute__((import_module("net"), import_name("host_sock_open"))) extern int32_t host_sock_open(
    int32_t domain, int32_t type);

__attribute__((import_module("net"), import_name("host_sock_resolve"))) extern int32_t host_sock_resolve(
    uint32_t name_ptr, uint32_t name_len, uint32_t ip_out);

__attribute__((import_module("net"), import_name("host_sock_close"))) extern int32_t host_sock_close(int32_t fd);

__attribute__((import_module("net"), import_name("host_sock_bind"))) extern int32_t host_sock_bind(
    int32_t fd, uint32_t ip_ptr, uint32_t port);

__attribute__((import_module("net"), import_name("host_sock_listen"))) extern int32_t host_sock_listen(
    int32_t fd, int32_t backlog);

// accept/recvfrom write a four-byte IP and a little-endian uint32_t port.
__attribute__((import_module("net"), import_name("host_sock_accept"))) extern int32_t host_sock_accept(
    int32_t fd, uint32_t ip_out, uint32_t port_out);

__attribute__((import_module("net"), import_name("host_sock_connect"))) extern int32_t host_sock_connect(
    int32_t fd, uint32_t ip_ptr, uint32_t port);

__attribute__((import_module("net"), import_name("host_sock_send"))) extern int32_t host_sock_send(
    int32_t fd, uint32_t buf_ptr, uint32_t len);

__attribute__((import_module("net"), import_name("host_sock_recv"))) extern int32_t host_sock_recv(
    int32_t fd, uint32_t buf_ptr, uint32_t len);

__attribute__((import_module("net"), import_name("host_sock_sendto"))) extern int32_t host_sock_sendto(
    int32_t fd, uint32_t buf_ptr, uint32_t len, uint32_t ip_ptr, uint32_t port);

__attribute__((import_module("net"), import_name("host_sock_recvfrom"))) extern int32_t host_sock_recvfrom(
    int32_t fd, uint32_t buf_ptr, uint32_t len, uint32_t ip_out, uint32_t port_out);

// Only scalar SOL_SOCKET options are forwarded, not Python callback pointers.
__attribute__((import_module("net"), import_name("host_sock_setsockopt"))) extern int32_t host_sock_setsockopt(
    int32_t fd, int32_t level, int32_t option, int32_t value);

// Milliseconds: -1 blocking, 0 nonblocking, positive values bound each I/O.
__attribute__((import_module("net"), import_name("host_sock_settimeout"))) extern int32_t host_sock_settimeout(
    int32_t fd, int32_t timeout_ms);

// Uses MP_STREAM_POLL_* bits, not the host OS poll constants.
__attribute__((import_module("net"), import_name("host_sock_poll"))) extern int32_t host_sock_poll(
    int32_t fd, uint32_t flags);

// Filesystem imports return a handle/count/status on success, or -MP_Exxx.
// Handles are host-owned tokens, not OS file descriptors. Status-only calls
// return zero. All pointers are borrowed wasm32 offsets, valid only during the
// import. Paths are slash-separated and not NUL-terminated; they arrive as the
// guest wrote them, so the host normalizes and rejects anything leaving root.
// I/O lengths and seek offsets are signed 32-bit. Seek positions are limited
// to INT32_MAX; stat sizes are uint32. This is not a file-size quota.
#define HOST_FS_READ (1 << 0)
#define HOST_FS_WRITE (1 << 1)
#define HOST_FS_APPEND (1 << 2)
#define HOST_FS_CREATE (1 << 3)
#define HOST_FS_TRUNCATE (1 << 4)

// stat writes three little-endian uint32_t: mode, size and mtime in seconds.
// Mode carries MP_S_IFDIR or MP_S_IFREG, not host permission bits.
__attribute__((import_module("fs"), import_name("host_fs_stat"))) extern int32_t host_fs_stat(
    uint32_t path_ptr, uint32_t path_len, uint32_t stat_out);

__attribute__((import_module("fs"), import_name("host_fs_open"))) extern int32_t host_fs_open(
    uint32_t path_ptr, uint32_t path_len, int32_t flags);

__attribute__((import_module("fs"), import_name("host_fs_close"))) extern int32_t host_fs_close(int32_t fd);

// read returns zero at end of file; write returns the count actually written.
__attribute__((import_module("fs"), import_name("host_fs_read"))) extern int32_t host_fs_read(
    int32_t fd, uint32_t buf_ptr, uint32_t len);

__attribute__((import_module("fs"), import_name("host_fs_write"))) extern int32_t host_fs_write(
    int32_t fd, uint32_t buf_ptr, uint32_t len);

// whence takes MP_SEEK_SET, MP_SEEK_CUR or MP_SEEK_END; pos_out gets a uint32_t.
__attribute__((import_module("fs"), import_name("host_fs_seek"))) extern int32_t host_fs_seek(
    int32_t fd, int32_t offset, int32_t whence, uint32_t pos_out);

__attribute__((import_module("fs"), import_name("host_fs_flush"))) extern int32_t host_fs_flush(int32_t fd);

__attribute__((import_module("fs"), import_name("host_fs_opendir"))) extern int32_t host_fs_opendir(
    uint32_t path_ptr, uint32_t path_len);

// readdir returns the name length written, or zero once the listing is drained.
// Names longer than name_cap are an error, not a truncation. mode_out gets a
// uint32_t holding MP_S_IFDIR or MP_S_IFREG.
__attribute__((import_module("fs"), import_name("host_fs_readdir"))) extern int32_t host_fs_readdir(
    int32_t dir, uint32_t name_out, uint32_t name_cap, uint32_t mode_out);

__attribute__((import_module("fs"), import_name("host_fs_closedir"))) extern int32_t host_fs_closedir(int32_t dir);

__attribute__((import_module("fs"), import_name("host_fs_mkdir"))) extern int32_t host_fs_mkdir(
    uint32_t path_ptr, uint32_t path_len);

__attribute__((import_module("fs"), import_name("host_fs_rmdir"))) extern int32_t host_fs_rmdir(
    uint32_t path_ptr, uint32_t path_len);

__attribute__((import_module("fs"), import_name("host_fs_remove"))) extern int32_t host_fs_remove(
    uint32_t path_ptr, uint32_t path_len);

__attribute__((import_module("fs"), import_name("host_fs_rename"))) extern int32_t host_fs_rename(
    uint32_t old_ptr, uint32_t old_len, uint32_t new_ptr, uint32_t new_len);

// Environment imports return a length/status on success, or -MP_Exxx. The
// environment is whatever the host chose to hand this instance, never the Go
// process environment, and writes are visible only to this guest.
//
// get returns the value's full length even when it exceeds value_cap, writing
// nothing in that case, so the guest can retry with a buffer that fits.
// -MP_ENOENT means the name is unset, which is not an error to the caller.
__attribute__((import_module("os"), import_name("host_env_get"))) extern int32_t host_env_get(
    uint32_t name_ptr, uint32_t name_len, uint32_t value_out, uint32_t value_cap);

__attribute__((import_module("os"), import_name("host_env_set"))) extern int32_t host_env_set(
    uint32_t name_ptr, uint32_t name_len, uint32_t value_ptr, uint32_t value_len);

__attribute__((import_module("os"), import_name("host_env_unset"))) extern int32_t host_env_unset(
    uint32_t name_ptr, uint32_t name_len);

// Reference bookkeeping lives on the host: slot allocation, deduplication by
// address, generations and acquisition counts. The guest keeps only the pin
// itself, because nothing in Go memory is traceable by this collector.
//
// go_ref_add takes the object's address and returns its id, or -1 if no slot
// is available. go_ref_free gives one acquisition back and returns 1 when that
// was the last one, meaning the guest should drop the pin.
__attribute__((import_module("env"), import_name("go_ref_add"))) extern int32_t go_ref_add(uint32_t addr);

__attribute__((import_module("env"), import_name("go_ref_free"))) extern int32_t go_ref_free(uint32_t id);

#endif
