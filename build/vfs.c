#include "vfs.h"

#include <stdint.h>
#include <string.h>

#include "host.h"
#include "py/objstr.h"
#include "py/runtime.h"
#include "py/stream.h"

// vfs.h requires MicroPython's types and port configuration first.
#include "extmod/vfs.h"

#if MICROPY_VFS

// Directory entries are read into a stack buffer one at a time.
#define HOST_FS_NAME_MAX (255)

// The host ABI returns -errno; VFS methods raise instead of reporting.
static int32_t fs_check(int32_t result) {
    if (result < 0) {
        mp_raise_OSError(result >= -4095 ? -result : MP_EIO);
    }
    return result;
}

static void fs_status(int32_t result) {
    if (fs_check(result) != 0) {
        mp_raise_OSError(MP_EIO);
    }
}

static uint32_t fs_offset(const void* ptr) { return (uint32_t)(uintptr_t)ptr; }

typedef struct _host_vfs_obj_t {
    mp_obj_base_t base;
} host_vfs_obj_t;

typedef struct _host_file_obj_t {
    mp_obj_base_t base;
    int32_t fd; // Negative once closed.
} host_file_obj_t;

extern const mp_obj_type_t host_fileio_type;
extern const mp_obj_type_t host_textio_type;

static mp_uint_t file_count(int32_t result, mp_uint_t len, int* errcode) {
    if (result < 0) {
        *errcode = result >= -4095 ? -result : MP_EIO;
        return MP_STREAM_ERROR;
    }
    if ((mp_uint_t)result > len) {
        *errcode = MP_EIO;
        return MP_STREAM_ERROR;
    }
    return (mp_uint_t)result;
}

static host_file_obj_t* file_open(mp_obj_t self_in, int* errcode) {
    host_file_obj_t* self = MP_OBJ_TO_PTR(self_in);
    if (self->fd < 0) {
        *errcode = MP_EBADF;
        return NULL;
    }
    return self;
}

static mp_uint_t file_read(mp_obj_t self_in, void* buf, mp_uint_t size, int* errcode) {
    host_file_obj_t* self = file_open(self_in, errcode);
    if (self == NULL) {
        return MP_STREAM_ERROR;
    }
    return file_count(host_fs_read(self->fd, fs_offset(buf), size), size, errcode);
}

static mp_uint_t file_write(mp_obj_t self_in, const void* buf, mp_uint_t size, int* errcode) {
    host_file_obj_t* self = file_open(self_in, errcode);
    if (self == NULL) {
        return MP_STREAM_ERROR;
    }
    mp_uint_t written = file_count(host_fs_write(self->fd, fs_offset(buf), size), size, errcode);
    if (written == 0 && size != 0) {
        *errcode = MP_EIO; // Avoid an endless retry on a non-progressing writer.
        return MP_STREAM_ERROR;
    }
    return written;
}

static mp_uint_t file_ioctl(mp_obj_t self_in, mp_uint_t request, uintptr_t arg, int* errcode) {
    host_file_obj_t* self = MP_OBJ_TO_PTR(self_in);

    if (request == MP_STREAM_CLOSE) {
        int32_t fd = self->fd;
        self->fd = -1; // Retire the token even if the underlying close fails.
        if (fd < 0) {
            return 0;
        }
        return file_count(host_fs_close(fd), 0, errcode);
    }

    if (file_open(self_in, errcode) == NULL) {
        return MP_STREAM_ERROR;
    }

    switch (request) {
        case MP_STREAM_FLUSH:
            return file_count(host_fs_flush(self->fd), 0, errcode);
        case MP_STREAM_SEEK: {
            struct mp_stream_seek_t* seek = (struct mp_stream_seek_t*)arg;
            uint32_t position = 0;
            int32_t result = host_fs_seek(
                self->fd, (int32_t)seek->offset, seek->whence, fs_offset(&position));
            if (file_count(result, 0, errcode) == MP_STREAM_ERROR) {
                return MP_STREAM_ERROR;
            }
            if (position > INT32_MAX) {
                *errcode = MP_EFBIG;
                return MP_STREAM_ERROR;
            }
            seek->offset = (mp_off_t)position;
            return 0;
        }
        default:
            *errcode = MP_EINVAL;
            return MP_STREAM_ERROR;
    }
}

static void file_print(const mp_print_t* print, mp_obj_t self_in, mp_print_kind_t kind) {
    (void)kind;
    host_file_obj_t* self = MP_OBJ_TO_PTR(self_in);
    mp_printf(print, "<io.%s %d>", mp_obj_get_type_str(self_in), self->fd);
}

static const mp_rom_map_elem_t file_locals_dict_table[] = {
    {MP_ROM_QSTR(MP_QSTR_read), MP_ROM_PTR(&mp_stream_read_obj)},
    {MP_ROM_QSTR(MP_QSTR_readinto), MP_ROM_PTR(&mp_stream_readinto_obj)},
    {MP_ROM_QSTR(MP_QSTR_readline), MP_ROM_PTR(&mp_stream_unbuffered_readline_obj)},
    {MP_ROM_QSTR(MP_QSTR_readlines), MP_ROM_PTR(&mp_stream_unbuffered_readlines_obj)},
    {MP_ROM_QSTR(MP_QSTR_write), MP_ROM_PTR(&mp_stream_write_obj)},
    {MP_ROM_QSTR(MP_QSTR_seek), MP_ROM_PTR(&mp_stream_seek_obj)},
    {MP_ROM_QSTR(MP_QSTR_tell), MP_ROM_PTR(&mp_stream_tell_obj)},
    {MP_ROM_QSTR(MP_QSTR_flush), MP_ROM_PTR(&mp_stream_flush_obj)},
    {MP_ROM_QSTR(MP_QSTR_close), MP_ROM_PTR(&mp_stream_close_obj)},
    {MP_ROM_QSTR(MP_QSTR___del__), MP_ROM_PTR(&mp_stream_close_obj)},
    {MP_ROM_QSTR(MP_QSTR___enter__), MP_ROM_PTR(&mp_identity_obj)},
    {MP_ROM_QSTR(MP_QSTR___exit__), MP_ROM_PTR(&mp_stream___exit___obj)},
};
static MP_DEFINE_CONST_DICT(file_locals_dict, file_locals_dict_table);

static const mp_stream_p_t fileio_stream_p = {
    .read = file_read,
    .write = file_write,
    .ioctl = file_ioctl,
};

MP_DEFINE_CONST_OBJ_TYPE(
    host_fileio_type,
    MP_QSTR_FileIO,
    MP_TYPE_FLAG_ITER_IS_STREAM,
    print,
    file_print,
    protocol,
    &fileio_stream_p,
    locals_dict,
    &file_locals_dict);

static const mp_stream_p_t textio_stream_p = {
    .read = file_read,
    .write = file_write,
    .ioctl = file_ioctl,
    .is_text = true,
};

MP_DEFINE_CONST_OBJ_TYPE(
    host_textio_type,
    MP_QSTR_TextIOWrapper,
    MP_TYPE_FLAG_ITER_IS_STREAM,
    print,
    file_print,
    protocol,
    &textio_stream_p,
    locals_dict,
    &file_locals_dict);

// Paths reach the host as the guest wrote them, with or without a leading
// slash, because mp_vfs_lookup_path only strips the mount point.
static const char* vfs_path(mp_obj_t path_in, size_t* len) {
    return mp_obj_str_get_data(path_in, len);
}

static mp_obj_t vfs_mount(mp_obj_t self_in, mp_obj_t readonly, mp_obj_t mkfs) {
    (void)self_in;
    (void)readonly;
    (void)mkfs;
    return mp_const_none;
}
static MP_DEFINE_CONST_FUN_OBJ_3(vfs_mount_obj, vfs_mount);

static mp_obj_t vfs_umount(mp_obj_t self_in) {
    (void)self_in;
    return mp_const_none;
}
static MP_DEFINE_CONST_FUN_OBJ_1(vfs_umount_obj, vfs_umount);

static mp_obj_t vfs_open(mp_obj_t self_in, mp_obj_t path_in, mp_obj_t mode_in) {
    (void)self_in;
    const mp_obj_type_t* type = &host_textio_type;
    int32_t flags = 0;
    bool base_mode = false;
    bool update = false;
    bool text_mode = false;
    size_t mode_len;
    const char* mode = mp_obj_str_get_data(mode_in, &mode_len);
    for (size_t i = 0; i < mode_len; ++i) {
        char ch = mode[i];
        if (ch == 'r' || ch == 'w' || ch == 'a') {
            if (base_mode) {
                mp_raise_ValueError(MP_ERROR_TEXT("invalid mode"));
            }
            base_mode = true;
        } else if (ch == '+') {
            if (update) {
                mp_raise_ValueError(MP_ERROR_TEXT("invalid mode"));
            }
            update = true;
        } else if (ch == 'b' || ch == 't') {
            if (text_mode) {
                mp_raise_ValueError(MP_ERROR_TEXT("invalid mode"));
            }
            text_mode = true;
        }
        switch (ch) {
            case 'r':
                flags |= HOST_FS_READ;
                break;
            case 'w':
                flags |= HOST_FS_WRITE | HOST_FS_CREATE | HOST_FS_TRUNCATE;
                break;
            case 'a':
                flags |= HOST_FS_WRITE | HOST_FS_CREATE | HOST_FS_APPEND;
                break;
            case '+':
                flags |= HOST_FS_READ | HOST_FS_WRITE;
                break;
            case 'b':
                type = &host_fileio_type;
                break;
            case 't':
                type = &host_textio_type;
                break;
            default:
                mp_raise_ValueError(MP_ERROR_TEXT("invalid mode"));
        }
    }
    if (!base_mode) {
        mp_raise_ValueError(MP_ERROR_TEXT("invalid mode"));
    }

    size_t len;
    const char* path = vfs_path(path_in, &len);
    host_file_obj_t* file = mp_obj_malloc_with_finaliser(host_file_obj_t, type);
    file->fd = -1; // Keep the object closable if the open below raises.
    file->fd = fs_check(host_fs_open(fs_offset(path), len, flags));
    return MP_OBJ_FROM_PTR(file);
}
static MP_DEFINE_CONST_FUN_OBJ_3(vfs_open_obj, vfs_open);

// There is no working directory: io/fs paths are always rooted at the mount.
static mp_obj_t vfs_chdir(mp_obj_t self_in, mp_obj_t path_in) {
    (void)self_in;
    size_t len;
    const char* path = vfs_path(path_in, &len);
    while (len > 0 && path[0] == '/') {
        ++path;
        --len;
    }
    if (len != 0) {
        mp_raise_OSError(MP_EOPNOTSUPP);
    }
    return mp_const_none;
}
static MP_DEFINE_CONST_FUN_OBJ_2(vfs_chdir_obj, vfs_chdir);

static mp_obj_t vfs_getcwd(mp_obj_t self_in) {
    (void)self_in;
    return MP_OBJ_NEW_QSTR(MP_QSTR__slash_);
}
static MP_DEFINE_CONST_FUN_OBJ_1(vfs_getcwd_obj, vfs_getcwd);

typedef struct _vfs_ilistdir_it_t {
    mp_obj_base_t base;
    mp_fun_1_t iternext;
    mp_fun_1_t finaliser;
    bool is_str;
    int32_t dir; // Negative once drained or closed.
} vfs_ilistdir_it_t;

static mp_obj_t vfs_ilistdir_it_iternext(mp_obj_t self_in) {
    vfs_ilistdir_it_t* self = MP_OBJ_TO_PTR(self_in);
    if (self->dir < 0) {
        return MP_OBJ_STOP_ITERATION;
    }

    char name[HOST_FS_NAME_MAX];
    uint32_t mode = 0;
    int32_t len =
        host_fs_readdir(self->dir, fs_offset(name), sizeof(name), fs_offset(&mode));
    if (len <= 0 || (uint32_t)len > sizeof(name)) {
        int32_t dir = self->dir;
        self->dir = -1;
        int32_t closed = host_fs_closedir(dir);
        if (len > 0) {
            mp_raise_OSError(MP_EIO); // The host overran the name buffer.
        }
        fs_check(len);
        fs_status(closed);
        return MP_OBJ_STOP_ITERATION;
    }

    mp_obj_tuple_t* entry = MP_OBJ_TO_PTR(mp_obj_new_tuple(3, NULL));
    entry->items[0] = self->is_str ? mp_obj_new_str(name, len)
                                   : mp_obj_new_bytes((const byte*)name, len);
    entry->items[1] = MP_OBJ_NEW_SMALL_INT(mode);
    entry->items[2] = MP_OBJ_NEW_SMALL_INT(0); // No inode numbers on the host.
    return MP_OBJ_FROM_PTR(entry);
}

static mp_obj_t vfs_ilistdir_it_del(mp_obj_t self_in) {
    vfs_ilistdir_it_t* self = MP_OBJ_TO_PTR(self_in);
    if (self->dir >= 0) {
        int32_t dir = self->dir;
        self->dir = -1;
        host_fs_closedir(dir);
    }
    return mp_const_none;
}

static mp_obj_t vfs_ilistdir(mp_obj_t self_in, mp_obj_t path_in) {
    (void)self_in;
    size_t len;
    const char* path = vfs_path(path_in, &len);
    vfs_ilistdir_it_t* iter = mp_obj_malloc_with_finaliser(
        vfs_ilistdir_it_t, &mp_type_polymorph_iter_with_finaliser);
    iter->iternext = vfs_ilistdir_it_iternext;
    iter->finaliser = vfs_ilistdir_it_del;
    iter->is_str = mp_obj_get_type(path_in) == &mp_type_str;
    iter->dir = -1; // Keep the finaliser safe if the open below raises.
    iter->dir = fs_check(host_fs_opendir(fs_offset(path), len));
    return MP_OBJ_FROM_PTR(iter);
}
static MP_DEFINE_CONST_FUN_OBJ_2(vfs_ilistdir_obj, vfs_ilistdir);

static mp_obj_t vfs_mkdir(mp_obj_t self_in, mp_obj_t path_in) {
    (void)self_in;
    size_t len;
    const char* path = vfs_path(path_in, &len);
    fs_status(host_fs_mkdir(fs_offset(path), len));
    return mp_const_none;
}
static MP_DEFINE_CONST_FUN_OBJ_2(vfs_mkdir_obj, vfs_mkdir);

static mp_obj_t vfs_rmdir(mp_obj_t self_in, mp_obj_t path_in) {
    (void)self_in;
    size_t len;
    const char* path = vfs_path(path_in, &len);
    fs_status(host_fs_rmdir(fs_offset(path), len));
    return mp_const_none;
}
static MP_DEFINE_CONST_FUN_OBJ_2(vfs_rmdir_obj, vfs_rmdir);

static mp_obj_t vfs_remove(mp_obj_t self_in, mp_obj_t path_in) {
    (void)self_in;
    size_t len;
    const char* path = vfs_path(path_in, &len);
    fs_status(host_fs_remove(fs_offset(path), len));
    return mp_const_none;
}
static MP_DEFINE_CONST_FUN_OBJ_2(vfs_remove_obj, vfs_remove);

static mp_obj_t vfs_rename(mp_obj_t self_in, mp_obj_t old_in, mp_obj_t new_in) {
    (void)self_in;
    size_t old_len;
    const char* old_path = vfs_path(old_in, &old_len);
    size_t new_len;
    const char* new_path = vfs_path(new_in, &new_len);
    fs_status(host_fs_rename(fs_offset(old_path), old_len, fs_offset(new_path), new_len));
    return mp_const_none;
}
static MP_DEFINE_CONST_FUN_OBJ_3(vfs_rename_obj, vfs_rename);

// The host reports mode, size and mtime; the remaining fields are unknowable
// through io/fs and stay zero, as they do on the block device filesystems.
static mp_obj_t vfs_stat(mp_obj_t self_in, mp_obj_t path_in) {
    (void)self_in;
    size_t len;
    const char* path = vfs_path(path_in, &len);
    uint32_t info[3] = {0, 0, 0};
    fs_status(host_fs_stat(fs_offset(path), len, fs_offset(info)));

    mp_obj_tuple_t* stat = MP_OBJ_TO_PTR(mp_obj_new_tuple(10, NULL));
    stat->items[0] = MP_OBJ_NEW_SMALL_INT(info[0]);
    stat->items[1] = MP_OBJ_NEW_SMALL_INT(0);
    stat->items[2] = MP_OBJ_NEW_SMALL_INT(0);
    stat->items[3] = MP_OBJ_NEW_SMALL_INT(0);
    stat->items[4] = MP_OBJ_NEW_SMALL_INT(0);
    stat->items[5] = MP_OBJ_NEW_SMALL_INT(0);
    stat->items[6] = mp_obj_new_int_from_uint(info[1]);
    stat->items[7] = mp_obj_new_int_from_uint(info[2]);
    stat->items[8] = mp_obj_new_int_from_uint(info[2]);
    stat->items[9] = mp_obj_new_int_from_uint(info[2]);
    return MP_OBJ_FROM_PTR(stat);
}
static MP_DEFINE_CONST_FUN_OBJ_2(vfs_stat_obj, vfs_stat);

// os.statvfs is bound to the mount table whatever MICROPY_PY_OS_STATVFS says,
// and io/fs reports no block or inode totals to answer it with.
static mp_obj_t vfs_statvfs(mp_obj_t self_in, mp_obj_t path_in) {
    (void)self_in;
    (void)path_in;
    mp_raise_OSError(MP_EOPNOTSUPP);
}
static MP_DEFINE_CONST_FUN_OBJ_2(vfs_statvfs_obj, vfs_statvfs);

// Import goes through the protocol slot rather than the stat method above, so
// a missing path costs one host call and never allocates.
static mp_import_stat_t vfs_import_stat(void* self_in, const char* path) {
    (void)self_in;
    uint32_t info[3] = {0, 0, 0};
    if (host_fs_stat(fs_offset(path), strlen(path), fs_offset(info)) != 0) {
        return MP_IMPORT_STAT_NO_EXIST;
    }
    return (info[0] & MP_S_IFDIR) ? MP_IMPORT_STAT_DIR : MP_IMPORT_STAT_FILE;
}

static const mp_rom_map_elem_t vfs_locals_dict_table[] = {
    {MP_ROM_QSTR(MP_QSTR_mount), MP_ROM_PTR(&vfs_mount_obj)},
    {MP_ROM_QSTR(MP_QSTR_umount), MP_ROM_PTR(&vfs_umount_obj)},
    {MP_ROM_QSTR(MP_QSTR_open), MP_ROM_PTR(&vfs_open_obj)},
    {MP_ROM_QSTR(MP_QSTR_chdir), MP_ROM_PTR(&vfs_chdir_obj)},
    {MP_ROM_QSTR(MP_QSTR_getcwd), MP_ROM_PTR(&vfs_getcwd_obj)},
    {MP_ROM_QSTR(MP_QSTR_ilistdir), MP_ROM_PTR(&vfs_ilistdir_obj)},
    {MP_ROM_QSTR(MP_QSTR_mkdir), MP_ROM_PTR(&vfs_mkdir_obj)},
    {MP_ROM_QSTR(MP_QSTR_remove), MP_ROM_PTR(&vfs_remove_obj)},
    {MP_ROM_QSTR(MP_QSTR_rename), MP_ROM_PTR(&vfs_rename_obj)},
    {MP_ROM_QSTR(MP_QSTR_rmdir), MP_ROM_PTR(&vfs_rmdir_obj)},
    {MP_ROM_QSTR(MP_QSTR_stat), MP_ROM_PTR(&vfs_stat_obj)},
    {MP_ROM_QSTR(MP_QSTR_statvfs), MP_ROM_PTR(&vfs_statvfs_obj)},
};
static MP_DEFINE_CONST_DICT(vfs_locals_dict, vfs_locals_dict_table);

static const mp_vfs_proto_t host_vfs_protocol = {
    .import_stat = vfs_import_stat,
};

static MP_DEFINE_CONST_OBJ_TYPE(
    host_vfs_type,
    MP_QSTR_HostVfs,
    MP_TYPE_FLAG_NONE,
    protocol,
    &host_vfs_protocol,
    locals_dict,
    &vfs_locals_dict);

// The singleton and its mount live outside the heap: nothing here is traced,
// and the guest has no constructor with which to make a second one.
static const host_vfs_obj_t host_vfs = {{&host_vfs_type}};

static mp_vfs_mount_t host_vfs_mount;

void host_vfs_init(void) {
    host_vfs_mount.str = "/";
    host_vfs_mount.len = 1;
    host_vfs_mount.obj = MP_OBJ_FROM_PTR(&host_vfs);
    host_vfs_mount.next = NULL;
    MP_STATE_VM(vfs_mount_table) = &host_vfs_mount;
    MP_STATE_VM(vfs_cur) = &host_vfs_mount;
}

#endif // MICROPY_VFS
