package vport

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TCGETS2
	ioctlSetTermios = unix.TCSETS2
)
