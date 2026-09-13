//go:build !windows

package engine

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"

	"github.com/oscar-investmatic/modeluplink-client/internal/hostexec"
)

func detectRAM() uint64 {
	if runtime.GOOS == "darwin" {
		if output, err := hostexec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
			var value uint64
			_, _ = fmt.Sscanf(strings.TrimSpace(string(output)), "%d", &value)
			return value
		}
	}
	data, err := os.ReadFile("/proc/meminfo")
	if err == nil {
		var kib uint64
		if _, err = fmt.Sscanf(string(data), "MemTotal: %d kB", &kib); err == nil {
			return kib << 10
		}
	}
	return 0
}

func detectStorage() uint64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(os.TempDir(), &stat); err != nil {
		return 0
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize)
}
