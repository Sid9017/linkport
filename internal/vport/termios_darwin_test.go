package vport

import "golang.org/x/sys/unix"

func setTestBaud(fd int, baud int) error {
	termios, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return err
	}
	termios.Ispeed = uint64(baud)
	termios.Ospeed = uint64(baud)
	return unix.IoctlSetTermios(fd, unix.TIOCSETA, termios)
}
