package vport

import "golang.org/x/sys/unix"

func setTestBaud(fd int, baud int) error {
	termios, err := unix.IoctlGetTermios(fd, unix.TCGETS2)
	if err != nil {
		return err
	}
	termios.Cflag &^= unix.CBAUD
	termios.Cflag |= unix.BOTHER
	termios.Ispeed = uint32(baud)
	termios.Ospeed = uint32(baud)
	return unix.IoctlSetTermios(fd, unix.TCSETS2, termios)
}
