//go:build !darwin && !linux && !windows

package vport

import (
	"fmt"
	"runtime"
)

// Open is not available on this platform.
func Open(options Options) (Port, error) {
	return nil, fmt.Errorf("vport: %s: %w", runtime.GOOS, ErrUnsupported)
}
