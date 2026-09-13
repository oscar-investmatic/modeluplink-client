package engine

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func detectRAM() uint64 {
	var m struct {
		Length, Load                                                                      uint32
		Physical, Available, PageFile, AvailablePage, Virtual, AvailableVirtual, Extended uint64
	}
	m.Length = uint32(unsafe.Sizeof(m))
	r, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx").Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0
	}
	return m.Physical
}
func detectStorage() uint64 {
	path, err := windows.UTF16PtrFromString(os.Getenv("LOCALAPPDATA"))
	if err != nil {
		return 0
	}
	var free, total, totalFree uint64
	if windows.GetDiskFreeSpaceEx(path, &free, &total, &totalFree) != nil {
		return 0
	}
	return free
}
