package vport

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ioctlSerialLSRMSTInsert = 0x001b007c

	com0comEscapeChar     = 0x8e
	com0comEventBuffer    = 16
	com0comTransferSize   = 4096
	com0comInitialLength  = 3 + 6 + 5
	com0comInsertResponse = 32

	dcbBinary        = 0x00000001
	commTimeoutMax   = 0xffffffff
	commTimeoutNever = 0xfffffffe
)

type com0comPort struct {
	handle       windows.Handle
	closeEvent   windows.Handle
	readEvent    windows.Handle
	writeEvent   windows.Handle
	controlEvent windows.Handle
	path         string

	events chan Event
	done   chan struct{}

	lifecycle sync.RWMutex
	closed    bool
	closeOnce sync.Once
	closeErr  error

	readMu         sync.Mutex
	readOverlapped windows.Overlapped
	readDone       uint32
	readBuf        []byte
	decoder        com0comDecoder
	state          com0comState

	writeMu         sync.Mutex
	writeOverlapped windows.Overlapped
	writeDone       uint32
	writeBuf        []byte

	controlMu         sync.Mutex
	controlOverlapped windows.Overlapped
	controlDone       uint32
	controlRequest    []byte
	controlResponse   []byte
}

// Open opens the linkport end of a com0com port pair. Local programs open
// the other end, named by options.Path.
//
// The pair must already exist. With com0com's default pinout the local
// program sees ModemStatus.CTS on its CTS input and ModemStatus.DSR on its
// DSR and DCD inputs; ModemStatus.DCD and ModemStatus.RI are not presented.
func Open(options Options) (Port, error) {
	if options.Path == "" || options.PairDevice == "" {
		return nil, errors.New("vport: Path and PairDevice are required on Windows")
	}
	name, err := windows.UTF16PtrFromString(`\\.\` + options.PairDevice)
	if err != nil {
		return nil, fmt.Errorf("vport: invalid port name %q: %w", options.PairDevice, err)
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil, fmt.Errorf("vport: com0com port %s not found; install com0com and create the port pair: %w", options.PairDevice, err)
	}
	if err != nil {
		return nil, fmt.Errorf("vport: open %s: %w", options.PairDevice, err)
	}
	port := &com0comPort{
		handle:          handle,
		path:            options.Path,
		events:          make(chan Event, com0comEventBuffer),
		done:            make(chan struct{}),
		readBuf:         make([]byte, com0comTransferSize),
		writeBuf:        make([]byte, com0comTransferSize),
		controlResponse: make([]byte, com0comInsertResponse),
		decoder:         com0comDecoder{escape: com0comEscapeChar},
	}
	if err := port.setup(options.PairDevice); err != nil {
		port.release()
		return nil, err
	}
	return port, nil
}

func (port *com0comPort) setup(device string) error {
	for _, event := range []*windows.Handle{&port.closeEvent, &port.readEvent, &port.writeEvent, &port.controlEvent} {
		created, err := windows.CreateEvent(nil, 1, 0, nil)
		if err != nil {
			return fmt.Errorf("vport: create event: %w", err)
		}
		*event = created
	}

	var dcb windows.DCB
	dcb.DCBlength = uint32(unsafe.Sizeof(dcb))
	if err := windows.GetCommState(port.handle, &dcb); err != nil {
		return fmt.Errorf("vport: read state of %s: %w", device, err)
	}
	dcb.Flags = dcbBinary
	dcb.ByteSize = 8
	dcb.Parity = 0
	dcb.StopBits = 0
	if err := windows.SetCommState(port.handle, &dcb); err != nil {
		return fmt.Errorf("vport: configure %s: %w", device, err)
	}

	timeouts := windows.CommTimeouts{
		ReadIntervalTimeout:        commTimeoutMax,
		ReadTotalTimeoutMultiplier: commTimeoutMax,
		ReadTotalTimeoutConstant:   commTimeoutNever,
	}
	if err := windows.SetCommTimeouts(port.handle, &timeouts); err != nil {
		return fmt.Errorf("vport: set timeouts of %s: %w", device, err)
	}

	port.controlRequest = com0comInsertRequest(
		com0comEscapeChar,
		com0comIoctlGet|com0comEnableMST|com0comEnableRBR|com0comEnableRLC,
	)
	returned, err := port.deviceControl(ioctlSerialLSRMSTInsert)
	if err != nil {
		return fmt.Errorf("vport: enable change reporting on %s: %w", device, err)
	}
	if returned != com0comInitialLength {
		return fmt.Errorf("vport: %s does not report paired port settings; it must be a com0com port", device)
	}
	initial := com0comDecoder{escape: com0comEscapeChar}
	initial.decode(port.controlResponse[:returned], func(code byte, payload []byte) {
		port.state.apply(code, payload)
	})
	return nil
}

func (port *com0comPort) deviceControl(code uint32) (uint32, error) {
	port.controlMu.Lock()
	defer port.controlMu.Unlock()
	port.controlOverlapped = windows.Overlapped{HEvent: port.controlEvent}
	err := windows.DeviceIoControl(
		port.handle,
		code,
		&port.controlRequest[0],
		uint32(len(port.controlRequest)),
		&port.controlResponse[0],
		uint32(len(port.controlResponse)),
		&port.controlDone,
		&port.controlOverlapped,
	)
	return port.finish(&port.controlOverlapped, err)
}

// finish waits for an overlapped operation that was just started and returns
// the number of bytes it transferred. Closing the port cancels the wait.
func (port *com0comPort) finish(overlapped *windows.Overlapped, startErr error) (uint32, error) {
	if startErr != nil && !errors.Is(startErr, windows.ERROR_IO_PENDING) {
		return 0, startErr
	}
	cancelled := false
	if startErr != nil {
		handles := []windows.Handle{overlapped.HEvent, port.closeEvent}
		signalled, _ := windows.WaitForMultipleObjects(handles, false, windows.INFINITE)
		if signalled != windows.WAIT_OBJECT_0 {
			cancelled = signalled == windows.WAIT_OBJECT_0+1
			windows.CancelIoEx(port.handle, overlapped)
		}
	}
	var transferred uint32
	err := windows.GetOverlappedResult(port.handle, overlapped, &transferred, true)
	if cancelled && errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
		return 0, os.ErrClosed
	}
	if err != nil {
		return 0, err
	}
	return transferred, nil
}

func (port *com0comPort) Path() string {
	return port.path
}

func (port *com0comPort) Capabilities() Capabilities {
	return Capabilities{LineSettings: true, ControlLines: true, ModemStatus: true}
}

func (port *com0comPort) Events() <-chan Event {
	return port.events
}

func (port *com0comPort) Read(buf []byte) (int, error) {
	port.lifecycle.RLock()
	defer port.lifecycle.RUnlock()
	if port.closed {
		return 0, os.ErrClosed
	}
	if len(buf) == 0 {
		return 0, nil
	}
	port.readMu.Lock()
	defer port.readMu.Unlock()
	limit := min(len(buf), len(port.readBuf))
	for {
		port.readOverlapped = windows.Overlapped{HEvent: port.readEvent}
		startErr := windows.ReadFile(port.handle, port.readBuf[:limit], &port.readDone, &port.readOverlapped)
		count, err := port.finish(&port.readOverlapped, startErr)
		if errors.Is(err, os.ErrClosed) {
			return 0, err
		}
		if err != nil {
			return 0, fmt.Errorf("vport: read %s: %w", port.path, err)
		}
		dataLength := port.decoder.decode(port.readBuf[:count], port.handleInsertion)
		if dataLength > 0 {
			copy(buf, port.readBuf[:dataLength])
			return dataLength, nil
		}
	}
}

func (port *com0comPort) handleInsertion(code byte, payload []byte) {
	event, changed := port.state.apply(code, payload)
	if !changed {
		return
	}
	select {
	case port.events <- event:
	case <-port.done:
	}
}

func (port *com0comPort) Write(buf []byte) (int, error) {
	port.lifecycle.RLock()
	defer port.lifecycle.RUnlock()
	if port.closed {
		return 0, os.ErrClosed
	}
	port.writeMu.Lock()
	defer port.writeMu.Unlock()
	written := 0
	for written < len(buf) {
		chunk := copy(port.writeBuf, buf[written:])
		port.writeOverlapped = windows.Overlapped{HEvent: port.writeEvent}
		startErr := windows.WriteFile(port.handle, port.writeBuf[:chunk], &port.writeDone, &port.writeOverlapped)
		count, err := port.finish(&port.writeOverlapped, startErr)
		if errors.Is(err, os.ErrClosed) {
			return written, err
		}
		if err != nil {
			return written, fmt.Errorf("vport: write %s: %w", port.path, err)
		}
		if count == 0 {
			return written, io.ErrShortWrite
		}
		written += int(count)
	}
	return written, nil
}

func (port *com0comPort) SetModemStatus(status ModemStatus) error {
	port.lifecycle.RLock()
	defer port.lifecycle.RUnlock()
	if port.closed {
		return os.ErrClosed
	}
	port.controlMu.Lock()
	defer port.controlMu.Unlock()
	requestToSend := uint32(windows.CLRRTS)
	if status.CTS {
		requestToSend = windows.SETRTS
	}
	dataTerminalReady := uint32(windows.CLRDTR)
	if status.DSR {
		dataTerminalReady = windows.SETDTR
	}
	if err := windows.EscapeCommFunction(port.handle, requestToSend); err != nil {
		return fmt.Errorf("vport: set CTS on %s: %w", port.path, err)
	}
	if err := windows.EscapeCommFunction(port.handle, dataTerminalReady); err != nil {
		return fmt.Errorf("vport: set DSR on %s: %w", port.path, err)
	}
	return nil
}

func (port *com0comPort) Close() error {
	port.closeOnce.Do(func() {
		close(port.done)
		windows.SetEvent(port.closeEvent)
		port.lifecycle.Lock()
		defer port.lifecycle.Unlock()
		port.closed = true
		port.closeErr = port.release()
		close(port.events)
	})
	return port.closeErr
}

func (port *com0comPort) release() error {
	err := windows.CloseHandle(port.handle)
	for _, event := range []windows.Handle{port.closeEvent, port.readEvent, port.writeEvent, port.controlEvent} {
		if event != 0 {
			windows.CloseHandle(event)
		}
	}
	return err
}
