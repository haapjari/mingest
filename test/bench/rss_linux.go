package bench

import (
	"os"
	"syscall"
)

// PeakRSS is an exited process's peak resident set in bytes, from
// getrusage. Linux reports ru_maxrss in KiB.
func PeakRSS(st *os.ProcessState) int64 {
	ru, ok := st.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0
	}

	return ru.Maxrss * 1024
}
