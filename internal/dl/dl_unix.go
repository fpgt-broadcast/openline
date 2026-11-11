//go:build !windows

// Package dl loads shared libraries at runtime (no cgo) so the bridge is a
// single cross-compiled binary that uses the system's libopus / NDI runtime.
package dl

import (
	"fmt"
	"strings"

	"github.com/ebitengine/purego"
)

// Open tries each candidate path/name in order and returns the first handle.
func Open(names ...string) (uintptr, error) {
	var errs []string
	for _, n := range names {
		if n == "" {
			continue
		}
		h, err := purego.Dlopen(n, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			return h, nil
		}
		errs = append(errs, err.Error())
	}
	return 0, fmt.Errorf("could not load any of %v: %s", names, strings.Join(errs, "; "))
}
