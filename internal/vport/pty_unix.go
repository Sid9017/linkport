//go:build darwin || linux

package vport

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

const (
	ptyEventBuffer       = 16
	ptySettingsInterval  = 100 * time.Millisecond
	ptyWriteRetryBackoff = time.Millisecond
)

type ptyPort struct {
	master    *os.File
	slave     *os.File
	masterFD  int
	slaveFD   int
	wakeRead  int
	wakeWrite int
	slaveName string
	link      string

	events      chan Event
	done        chan struct{}
	pollStopped chan struct{}

	lifecycle sync.RWMutex
	closed    bool
	closeOnce sync.Once
	closeErr  error

	settingsMu sync.Mutex
	settings   LineSettings
}

// Open creates a pseudo-terminal and returns its master side. Local programs
// open the slave side through Path.
//
// The slave is kept open and in raw mode for the lifetime of the port, so
// programs can come and go without the port hanging up, and nothing written
// to the port is echoed back or translated.
func Open(options Options) (Port, error) {
	master, slave, err := pty.Open()
	if err != nil {
		return nil, fmt.Errorf("vport: open pty: %w", err)
	}
	port := &ptyPort{
		master:      master,
		slave:       slave,
		masterFD:    int(master.Fd()),
		slaveFD:     int(slave.Fd()),
		wakeRead:    -1,
		wakeWrite:   -1,
		slaveName:   slave.Name(),
		events:      make(chan Event, ptyEventBuffer),
		done:        make(chan struct{}),
		pollStopped: make(chan struct{}),
	}
	if err := port.setup(options); err != nil {
		port.release()
		return nil, err
	}
	go port.pollSettings()
	return port, nil
}

func (port *ptyPort) setup(options Options) error {
	if err := makeRaw(port.slaveFD); err != nil {
		return fmt.Errorf("vport: set raw mode: %w", err)
	}
	if err := unix.SetNonblock(port.masterFD, true); err != nil {
		return fmt.Errorf("vport: set non-blocking: %w", err)
	}
	var pipeFDs [2]int
	if err := unix.Pipe(pipeFDs[:]); err != nil {
		return fmt.Errorf("vport: create wake pipe: %w", err)
	}
	port.wakeRead, port.wakeWrite = pipeFDs[0], pipeFDs[1]
	unix.CloseOnExec(port.wakeRead)
	unix.CloseOnExec(port.wakeWrite)
	if max(port.masterFD, port.wakeRead) >= unix.FD_SETSIZE {
		return errors.New("vport: too many open files to wait on the pty")
	}
	settings, err := readLineSettings(port.slaveFD)
	if err != nil {
		return fmt.Errorf("vport: read line settings: %w", err)
	}
	port.settings = settings
	if options.Path != "" {
		if err := createLink(port.slaveName, options.Path); err != nil {
			return err
		}
		port.link = options.Path
	}
	return nil
}

func createLink(target, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("vport: create directory for %s: %w", path, err)
	}
	info, err := os.Lstat(path)
	switch {
	case err == nil && info.Mode()&os.ModeSymlink == 0:
		return fmt.Errorf("vport: %s exists and is not a symlink", path)
	case err == nil:
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("vport: replace %s: %w", path, err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("vport: inspect %s: %w", path, err)
	}
	if err := os.Symlink(target, path); err != nil {
		return fmt.Errorf("vport: link %s: %w", path, err)
	}
	return nil
}

func (port *ptyPort) Path() string {
	if port.link != "" {
		return port.link
	}
	return port.slaveName
}

func (port *ptyPort) Capabilities() Capabilities {
	return Capabilities{LineSettings: true}
}

func (port *ptyPort) Events() <-chan Event {
	return port.events
}

func (port *ptyPort) SetModemStatus(status ModemStatus) error {
	return fmt.Errorf("vport: a pty has no modem lines: %w", ErrUnsupported)
}

func (port *ptyPort) Read(buf []byte) (int, error) {
	port.lifecycle.RLock()
	defer port.lifecycle.RUnlock()
	if port.closed {
		return 0, os.ErrClosed
	}
	if len(buf) == 0 {
		return 0, nil
	}
	for {
		if err := port.wait(false); err != nil {
			return 0, err
		}
		port.checkSettings()
		count, err := unix.Read(port.masterFD, buf)
		switch {
		case err == unix.EAGAIN || err == unix.EINTR:
			continue
		case err != nil:
			return 0, fmt.Errorf("vport: read pty: %w", err)
		case count == 0:
			return 0, io.EOF
		}
		return count, nil
	}
}

func (port *ptyPort) Write(buf []byte) (int, error) {
	port.lifecycle.RLock()
	defer port.lifecycle.RUnlock()
	if port.closed {
		return 0, os.ErrClosed
	}
	written := 0
	for written < len(buf) {
		if err := port.wait(true); err != nil {
			return written, err
		}
		count, err := unix.Write(port.masterFD, buf[written:])
		switch {
		case err == unix.EAGAIN:
			time.Sleep(ptyWriteRetryBackoff)
			continue
		case err == unix.EINTR:
			continue
		case err != nil:
			return written, fmt.Errorf("vport: write pty: %w", err)
		}
		written += count
	}
	return written, nil
}

func (port *ptyPort) wait(forWrite bool) error {
	highest := max(port.masterFD, port.wakeRead)
	for {
		var readSet, writeSet unix.FdSet
		readSet.Set(port.wakeRead)
		if forWrite {
			writeSet.Set(port.masterFD)
		} else {
			readSet.Set(port.masterFD)
		}
		_, err := unix.Select(highest+1, &readSet, &writeSet, nil, nil)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return fmt.Errorf("vport: wait on pty: %w", err)
		}
		if readSet.IsSet(port.wakeRead) {
			return os.ErrClosed
		}
		return nil
	}
}

func (port *ptyPort) pollSettings() {
	defer close(port.pollStopped)
	ticker := time.NewTicker(ptySettingsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-port.done:
			return
		case <-ticker.C:
			port.checkSettings()
		}
	}
}

func (port *ptyPort) checkSettings() {
	settings, err := readLineSettings(port.slaveFD)
	if err != nil {
		return
	}
	port.settingsMu.Lock()
	defer port.settingsMu.Unlock()
	if settings == port.settings {
		return
	}
	port.settings = settings
	select {
	case port.events <- Event{Kind: EventLineSettings, Line: settings}:
	case <-port.done:
	}
}

func (port *ptyPort) Close() error {
	port.closeOnce.Do(func() {
		close(port.done)
		unix.Close(port.wakeWrite)
		port.wakeWrite = -1
		<-port.pollStopped
		port.lifecycle.Lock()
		defer port.lifecycle.Unlock()
		port.closed = true
		port.closeErr = port.release()
		close(port.events)
	})
	return port.closeErr
}

func (port *ptyPort) release() error {
	if port.link != "" {
		if target, err := os.Readlink(port.link); err == nil && target == port.slaveName {
			os.Remove(port.link)
		}
	}
	if port.wakeWrite >= 0 {
		unix.Close(port.wakeWrite)
	}
	if port.wakeRead >= 0 {
		unix.Close(port.wakeRead)
	}
	return errors.Join(port.master.Close(), port.slave.Close())
}

func makeRaw(fd int) error {
	termios, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return err
	}
	termios.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	termios.Oflag &^= unix.OPOST
	termios.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	termios.Cflag &^= unix.CSIZE | unix.PARENB
	termios.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL
	termios.Cc[unix.VMIN] = 1
	termios.Cc[unix.VTIME] = 0
	return unix.IoctlSetTermios(fd, ioctlSetTermios, termios)
}

func readLineSettings(fd int) (LineSettings, error) {
	termios, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return LineSettings{}, err
	}
	settings := LineSettings{BaudRate: int(termios.Ospeed), DataBits: 8}
	switch termios.Cflag & unix.CSIZE {
	case unix.CS5:
		settings.DataBits = 5
	case unix.CS6:
		settings.DataBits = 6
	case unix.CS7:
		settings.DataBits = 7
	}
	if termios.Cflag&unix.PARENB != 0 {
		settings.Parity = ParityEven
		if termios.Cflag&unix.PARODD != 0 {
			settings.Parity = ParityOdd
		}
	}
	if termios.Cflag&unix.CSTOPB != 0 {
		settings.StopBits = StopBits2
	}
	return settings, nil
}
