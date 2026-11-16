//go:build !darwin && !linux && !windows && !freebsd && !openbsd

package serialport

func enumerateSystem() ([]Port, error) {
	return nil, ErrUnsupported
}
