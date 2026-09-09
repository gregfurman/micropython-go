#include "network.h"

#include <stdint.h>
#include <string.h>

#include "host.h"
#include "py/runtime.h"
#include "py/stream.h"

// modnetwork.h requires MicroPython's types and port configuration first.
#include "extmod/modnetwork.h"

// The host ABI returns -errno; the NIC protocol returns -1 and sets errno.
static int socket_result(int32_t result, int* err) {
    if (result < 0) {
        *err = result >= -4095 ? -result : MP_EIO;
        return -1;
    }
    return result;
}

static int socket_status(int32_t result, int* err) {
    if (result > 0) {
        *err = MP_EIO;
        return -1;
    }
    return socket_result(result, err);
}

static bool socket_valid(mod_network_socket_obj_t* socket, int* err) {
    if (socket->fileno < 0 || socket->state == MOD_NETWORK_SS_CLOSED) {
        *err = MP_EBADF;
        return false;
    }
    return true;
}

static bool socket_port(mp_uint_t port, int* err) {
    if (port > UINT16_MAX) {
        *err = MP_EINVAL;
        return false;
    }
    return true;
}

static int socket_descriptor(int32_t fd, int* err) {
    if (fd > INT16_MAX) {
        host_sock_close(fd);
        *err = MP_EMFILE;
        return -1;
    }
    return socket_result(fd, err);
}

static mp_uint_t socket_count(int32_t result, mp_uint_t len, int* err) {
    if (result >= 0 && (uint32_t)result > len) {
        *err = MP_EIO;
        return MP_STREAM_ERROR;
    }
    return socket_result(result, err);
}

static int network_resolve(mp_obj_t nic, const char* name, mp_uint_t len, uint8_t* ip) {
    (void)nic;
    if (len == 0 || memchr(name, 0, len) != NULL) {
        return MP_EINVAL;
    }
    int err = 0;
    // Unlike socket callbacks, gethostbyname returns the positive errno itself.
    socket_status(host_sock_resolve((uint32_t)name, len, (uint32_t)ip), &err);
    return err;
}

static int network_open(mod_network_socket_obj_t* socket, int* err) {
    int fd = -1;
    if (socket->state == MOD_NETWORK_SS_CLOSED) {
        *err = MP_EBADF;
    } else if (socket->domain != MOD_NETWORK_AF_INET) {
        *err = MP_EAFNOSUPPORT;
    } else if ((socket->type != MOD_NETWORK_SOCK_STREAM && socket->type != MOD_NETWORK_SOCK_DGRAM) ||
               (socket->proto != 0 && socket->proto != (socket->type == MOD_NETWORK_SOCK_STREAM ? 6 : 17))) {
        *err = MP_EOPNOTSUPP;
    } else {
        fd = socket_descriptor(host_sock_open(socket->domain, socket->type), err);
    }
    socket->fileno = fd;
    if (fd < 0) {
        // modsocket selects the NIC before opening. Undo that on failure so a
        // retry opens again, and the finaliser never closes an invalid token.
        socket->nic = MP_OBJ_NULL;
        socket->nic_protocol = NULL;
        return -1;
    }
    return 0;
}

static void network_close(mod_network_socket_obj_t* socket) {
    int fd = socket->fileno;
    socket->fileno = -1;
    socket->state = MOD_NETWORK_SS_CLOSED;
    if (fd >= 0) {
        // The NIC close callback cannot return an error. The host must release
        // the token even if closing the underlying resource reports an error.
        host_sock_close(fd);
    }
}

static int network_bind(mod_network_socket_obj_t* socket, byte* ip, mp_uint_t port, int* err) {
    if (!socket_valid(socket, err) || !socket_port(port, err)) {
        return -1;
    }
    int result = socket_status(host_sock_bind(socket->fileno, (uint32_t)ip, port), err);
    if (result == 0) {
        socket->bound = true;
    }
    return result;
}

static int network_listen(mod_network_socket_obj_t* socket, mp_int_t backlog, int* err) {
    if (!socket_valid(socket, err)) {
        return -1;
    }
    return socket_status(host_sock_listen(socket->fileno, backlog), err);
}

static int network_accept(
    mod_network_socket_obj_t* socket, mod_network_socket_obj_t* client, byte* ip, mp_uint_t* port, int* err) {
    if (!socket_valid(socket, err)) {
        return -1;
    }
    uint32_t peer_port = 0;
    int fd = socket_descriptor(host_sock_accept(socket->fileno, (uint32_t)ip, (uint32_t)&peer_port), err);
    if (fd < 0) {
        return -1;
    }
    if (!socket_port(peer_port, err)) {
        host_sock_close(fd);
        return -1;
    }
    client->fileno = fd;
    client->state = MOD_NETWORK_SS_CONNECTED;
    *port = peer_port;
    return 0;
}

static int network_connect(mod_network_socket_obj_t* socket, byte* ip, mp_uint_t port, int* err) {
    if (!socket_valid(socket, err) || !socket_port(port, err)) {
        return -1;
    }
    return socket_status(host_sock_connect(socket->fileno, (uint32_t)ip, port), err);
}

static mp_uint_t network_send(mod_network_socket_obj_t* socket, const byte* buf, mp_uint_t len, int* err) {
    if (!socket_valid(socket, err)) {
        return MP_STREAM_ERROR;
    }
    int32_t result = host_sock_send(socket->fileno, (uint32_t)buf, len);
    if (result == 0 && len != 0) {
        // modsocket.sendall retries partial sends; zero must not spin forever.
        *err = MP_EPIPE;
        return MP_STREAM_ERROR;
    }
    return socket_count(result, len, err);
}

static mp_uint_t network_recv(mod_network_socket_obj_t* socket, byte* buf, mp_uint_t len, int* err) {
    if (!socket_valid(socket, err)) {
        return MP_STREAM_ERROR;
    }
    return socket_count(host_sock_recv(socket->fileno, (uint32_t)buf, len), len, err);
}

static mp_uint_t network_sendto(
    mod_network_socket_obj_t* socket, const byte* buf, mp_uint_t len, byte* ip, mp_uint_t port, int* err) {
    if (!socket_valid(socket, err) || !socket_port(port, err)) {
        return MP_STREAM_ERROR;
    }
    return socket_count(host_sock_sendto(socket->fileno, (uint32_t)buf, len, (uint32_t)ip, port), len, err);
}

static mp_uint_t network_recvfrom(
    mod_network_socket_obj_t* socket, byte* buf, mp_uint_t len, byte* ip, mp_uint_t* port, int* err) {
    if (!socket_valid(socket, err)) {
        return MP_STREAM_ERROR;
    }
    uint32_t peer_port = 0;
    mp_uint_t result = socket_count(
        host_sock_recvfrom(socket->fileno, (uint32_t)buf, len, (uint32_t)ip, (uint32_t)&peer_port), len, err);
    if (result == MP_STREAM_ERROR || !socket_port(peer_port, err)) {
        return MP_STREAM_ERROR;
    }
    *port = peer_port;
    return result;
}

static int network_setsockopt(
    mod_network_socket_obj_t* socket, mp_uint_t level, mp_uint_t option, const void* value, mp_uint_t len, int* err) {
    if (!socket_valid(socket, err)) {
        return -1;
    }
    // modsocket also accepts Python callback objects here. Never expose those
    // pointers to the host as an option buffer.
    if (level != MOD_NETWORK_SOL_SOCKET || len != sizeof(int32_t) ||
        (option != MOD_NETWORK_SO_REUSEADDR && option != MOD_NETWORK_SO_BROADCAST &&
            option != MOD_NETWORK_SO_KEEPALIVE)) {
        *err = MP_EOPNOTSUPP;
        return -1;
    }
    int32_t scalar;
    memcpy(&scalar, value, sizeof(scalar));
    return socket_status(host_sock_setsockopt(socket->fileno, level, option, scalar), err);
}

static int network_settimeout(mod_network_socket_obj_t* socket, mp_uint_t timeout, int* err) {
    if (!socket_valid(socket, err)) {
        return -1;
    }
    if (timeout != UINT32_MAX && timeout > INT32_MAX) {
        *err = MP_EINVAL;
        return -1;
    }
    int result = socket_status(host_sock_settimeout(socket->fileno, (int32_t)timeout), err);
    if (result == 0) {
        socket->timeout = (int32_t)timeout;
    } else if (socket->state == MOD_NETWORK_SS_NEW && !socket->bound) {
        // NIC selection applies a saved timeout after open. If that fails,
        // discard the new token so a later connect cannot skip the timeout.
        network_close(socket);
        socket->nic = MP_OBJ_NULL;
        socket->nic_protocol = NULL;
    }
    return result;
}

static int network_ioctl(mod_network_socket_obj_t* socket, mp_uint_t request, mp_uint_t arg, int* err) {
    if (!socket_valid(socket, err)) {
        return -1;
    }
    if (request != MP_STREAM_POLL) {
        *err = MP_EOPNOTSUPP;
        return -1;
    }
    return socket_result(host_sock_poll(socket->fileno, arg), err);
}

static const mod_network_nic_protocol_t host_network_protocol = {
    .gethostbyname = network_resolve,
    .socket = network_open,
    .close = network_close,
    .bind = network_bind,
    .listen = network_listen,
    .accept = network_accept,
    .connect = network_connect,
    .send = network_send,
    .recv = network_recv,
    .sendto = network_sendto,
    .recvfrom = network_recvfrom,
    .setsockopt = network_setsockopt,
    .settimeout = network_settimeout,
    .ioctl = network_ioctl,
};

static MP_DEFINE_CONST_OBJ_TYPE(
    host_network_type, MP_QSTR_HostNetwork, MP_TYPE_FLAG_NONE, protocol, &host_network_protocol);

static const mp_obj_base_t host_network = {&host_network_type};

void host_network_init(void) { mod_network_register_nic(MP_OBJ_FROM_PTR(&host_network)); }
