package host

type WasiStub struct {
	env *Host
}

func (w *WasiStub) Xfd_write(fd, iovs_ptr, iovs_len, nwritten_ptr int32) int32         { return 0 }
func (w *WasiStub) Xfd_close(fd int32) int32                                           { return 0 }
func (w *WasiStub) Xfd_fdstat_get(fd, stat_ptr int32) int32                            { return 0 }
func (w *WasiStub) Xfd_seek(fd int32, offset int64, whence, newoffset_ptr int32) int32 { return 0 }
