#include "pymodule.h"

#include "py/objmodule.h"
#include "py/qstr.h"
#include "py/runtime.h"

// Return the module at path, creating every prefix and linking each child into
// its parent. mp_obj_new_module also registers the fully-qualified module in
// sys.modules, so ordinary imports find the same objects.
mp_obj_t get_or_create_module(const char* path, size_t path_len) {
    if (path_len == 0) {
        mp_raise_ValueError(MP_ERROR_TEXT("empty module name"));
    }

    mp_obj_t parent = MP_OBJ_NULL;
    size_t part_start = 0;
    for (size_t end = 0; end <= path_len; ++end) {
        if (end != path_len && path[end] != '.') {
            continue;
        }
        if (end == part_start) {
            mp_raise_ValueError(MP_ERROR_TEXT("invalid module name"));
        }

        mp_obj_t module = mp_obj_new_module(qstr_from_strn(path, end));
        if (!mp_obj_is_type(module, &mp_type_module)) {
            mp_raise_TypeError(MP_ERROR_TEXT("module name is already occupied"));
        }

        if (parent != MP_OBJ_NULL) {
            mp_obj_module_t* parent_module = MP_OBJ_TO_PTR(parent);
            qstr part_name = qstr_from_strn(path + part_start, end - part_start);
            mp_obj_dict_store(MP_OBJ_FROM_PTR(parent_module->globals), MP_OBJ_NEW_QSTR(part_name), module);

            // A parent containing a child is a package. This matters for
            // `from parent import child` and for ports with external imports.
            qstr path_name = qstr_from_strn("__path__", 8);
            if (mp_map_lookup(&parent_module->globals->map, MP_OBJ_NEW_QSTR(path_name), MP_MAP_LOOKUP) == NULL) {
                mp_obj_dict_store(
                    MP_OBJ_FROM_PTR(parent_module->globals), MP_OBJ_NEW_QSTR(path_name), mp_obj_new_list(0, NULL));
            }
        }
        parent = module;
        part_start = end + 1;
    }
    return parent;
}

void module_store_attr(mp_obj_t module, const char* name, size_t name_len, mp_obj_t value) {
    mp_obj_module_t* mod = MP_OBJ_TO_PTR(module);
    mp_obj_dict_store(MP_OBJ_FROM_PTR(mod->globals), MP_OBJ_NEW_QSTR(qstr_from_strn(name, name_len)), value);
}
