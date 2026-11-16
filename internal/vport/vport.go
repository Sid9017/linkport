// Package vport provides a local virtual serial port that other programs on
// this machine can open as if it were a real one.
//
// The caller holds the linkport end of the port: bytes written by the local
// program come out of Read, and bytes passed to Write are delivered to the
// local program. Serial settings the local program applies are reported
// through Events so they can be forwarded to the remote port.
//
// macOS and Linux use a pseudo-terminal; Windows uses a com0com port pair.
package vport

import (
	"errors"
	"io"
)

// ErrUnsupported is returned by operations the platform backend cannot do.
var ErrUnsupported = errors.ErrUnsupported

// Parity is the parity mode of a serial line.
type Parity int

const (
	ParityNone Parity = iota
	ParityOdd
	ParityEven
	ParityMark
	ParitySpace
)

// StopBits is the number of stop bits of a serial line.
type StopBits int

const (
	StopBits1 StopBits = iota
	StopBits1Half
	StopBits2
)

// LineSettings is the framing the local program configured on the port.
type LineSettings struct {
	BaudRate int
	DataBits int
	Parity   Parity
	StopBits StopBits
}

// ControlLines are the output lines driven by the local program.
type ControlLines struct {
	DTR bool
	RTS bool
}

// ModemStatus are the input lines the local program observes.
type ModemStatus struct {
	CTS bool
	DSR bool
	DCD bool
	RI  bool
}

// EventKind tells which field of an Event is meaningful.
type EventKind int

const (
	EventLineSettings EventKind = iota + 1
	EventControlLines
)

// Event reports a change the local program made to the port.
type Event struct {
	Kind    EventKind
	Line    LineSettings
	Control ControlLines
}

// Capabilities describes how much of a real serial port a backend emulates.
type Capabilities struct {
	// LineSettings is true when baud rate and framing changes are reported.
	LineSettings bool
	// ControlLines is true when DTR and RTS changes are reported.
	ControlLines bool
	// ModemStatus is true when SetModemStatus reaches the local program.
	ModemStatus bool
}

// Options configures Open.
type Options struct {
	// Path is where local programs find the port.
	//
	// On macOS and Linux it is an optional symlink created to point at the
	// pseudo-terminal; when empty, the pseudo-terminal's own path is used.
	//
	// On Windows it is the name of the com0com port that local programs
	// open, for example "COM10". It is required and only used for reporting.
	Path string

	// PairDevice is the com0com port linkport itself opens, the other end of
	// the pair that Path belongs to, for example "CNCB0". Windows only.
	PairDevice string
}

// Port is the linkport end of a virtual serial port.
//
// Read and Write block until they make progress or the port is closed, in
// which case they return os.ErrClosed. One Read and one Write may run
// concurrently with each other and with Close.
type Port interface {
	io.ReadWriteCloser

	// Path returns what a local program opens to use the port.
	Path() string

	// Capabilities reports what this backend can emulate.
	Capabilities() Capabilities

	// Events delivers changes made by the local program. Only changes are
	// reported; the state at Open is not. A change made before some bytes
	// were written is queued before Read returns those bytes. The channel
	// must be drained, and is closed by Close.
	Events() <-chan Event

	// SetModemStatus presents input line states to the local program.
	SetModemStatus(status ModemStatus) error
}
