package bench

import (
	"os"
	"syscall"
)

// PeakRSS is an exited process's peak resident set in bytes, from
// getrusage. macOS reports ru_maxrss in bytes.
func PeakRSS(st *os.ProcessState) int64 {
	ru, ok := st.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0
	}

	return ru.Maxrss
}
