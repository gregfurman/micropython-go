#include "host.h"
#include "py/mperrno.h"

// Values longer than this cost a second host call rather than a guess.
#define HOST_ENV_INLINE_MAX (128)

static int32_t env_check(int32_t result) {
    if (result < 0) {
        mp_raise_OSError(result >= -4095 ? -result : MP_EIO);
    }
    return result;
}

static void env_status(int32_t result) {
    if (env_check(result) != 0) {
        mp_raise_OSError(MP_EIO);
    }
}

static mp_obj_t mp_os_getenv(size_t n_args, const mp_obj_t* args) {
    size_t name_len;
    const char* name = mp_obj_str_get_data(args[0], &name_len);

    char inline_value[HOST_ENV_INLINE_MAX];
    int32_t len =
        host_env_get((uint32_t)(uintptr_t)name, name_len, (uint32_t)(uintptr_t)inline_value, sizeof(inline_value));
    if (len == -MP_ENOENT) {
        return n_args == 2 ? args[1] : mp_const_none;
    }
    if ((uint32_t)env_check(len) <= sizeof(inline_value)) {
        return mp_obj_new_str(inline_value, len);
    }

    vstr_t value;
    vstr_init_len(&value, len);
    int32_t again = host_env_get((uint32_t)(uintptr_t)name, name_len, (uint32_t)(uintptr_t)value.buf, len);
    if (env_check(again) != len) {
        // The value changed between the sizing call and this one.
        mp_raise_OSError(MP_EAGAIN);
    }
    return mp_obj_new_str_from_vstr(&value);
}
static MP_DEFINE_CONST_FUN_OBJ_VAR_BETWEEN(mp_os_getenv_obj, 1, 2, mp_os_getenv);

static mp_obj_t mp_os_putenv(mp_obj_t name_in, mp_obj_t value_in) {
    size_t name_len;
    const char* name = mp_obj_str_get_data(name_in, &name_len);
    size_t value_len;
    const char* value = mp_obj_str_get_data(value_in, &value_len);
    env_status(host_env_set((uint32_t)(uintptr_t)name, name_len, (uint32_t)(uintptr_t)value, value_len));
    return mp_const_none;
}
static MP_DEFINE_CONST_FUN_OBJ_2(mp_os_putenv_obj, mp_os_putenv);

static mp_obj_t mp_os_unsetenv(mp_obj_t name_in) {
    size_t name_len;
    const char* name = mp_obj_str_get_data(name_in, &name_len);
    env_status(host_env_unset((uint32_t)(uintptr_t)name, name_len));
    return mp_const_none;
}
static MP_DEFINE_CONST_FUN_OBJ_1(mp_os_unsetenv_obj, mp_os_unsetenv);
