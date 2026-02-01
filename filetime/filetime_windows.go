package filetime

import (
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var pool = &sync.Pool{
	New: func() interface{} {
		return &syscall.Filetime{}
	},
}

func uint64FromFiletime(filetime *syscall.Filetime) uint64 {
	result := *(*uint64)(unsafe.Pointer(filetime))
	return result
}

func Timestamp(t time.Time) uint64 {
	filetime := pool.Get().(*syscall.Filetime)
	defer pool.Put(filetime)
	*filetime = syscall.NsecToFiletime(t.UnixNano())
	return uint64FromFiletime(filetime)
}

func Filetime(t syscall.Filetime) uint64 {
	return uint64FromFiletime(&t)
}

func FiletimeFromRaw(t uint64) syscall.Filetime {
	filetime := pool.Get().(*syscall.Filetime)
	defer pool.Put(filetime)
	t1 := (*uint64)(unsafe.Pointer(filetime))
	*t1 = t
	return *filetime
}

func TimeFromRaw(t uint64) time.Time {
	filetime := FiletimeFromRaw(t)
	return time.Unix(0, filetime.Nanoseconds())
}
