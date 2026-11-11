//go:build windows

package dl

import (
	"fmt"
	"strings"
	"syscall"
)

// Open tries each candidate path/name in order and returns the first handle.
func Open(names ...string) (uintptr, error) {
	var errs []string
	for _, n := range names {
		if n == "" {
			continue
		}
		h, err := syscall.LoadLibrary(n)
		if err == nil {
			return uintptr(h), nil
		}
		errs = append(errs, n+": "+err.Error())
	}
	return 0, fmt.Errorf("could not load any of %v: %s", names, strings.Join(errs, "; "))
}
