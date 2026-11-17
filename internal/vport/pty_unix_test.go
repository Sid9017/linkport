//go:build darwin || linux

package vport

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const testTimeout = 3 * time.Second

func openTestPort(t *testing.T) (Port, int) {
	t.Helper()
	port, err := Open(Options{Path: filepath.Join(t.TempDir(), "dev", "board")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { port.Close() })
	appFD, err := unix.Open(port.Path(), unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", port.Path(), err)
	}
	t.Cleanup(func() { unix.Close(appFD) })
	return port, appFD
}

type readResult struct {
	data []byte
	err  error
}

func readExactly(t *testing.T, name string, length int, read func([]byte) (int, error)) []byte {
	t.Helper()
	results := make(chan readResult, 1)
	go func() {
		data := make([]byte, 0, length)
		buf := make([]byte, length)
		for len(data) < length {
			count, err := read(buf[:length-len(data)])
			if err != nil {
				results <- readResult{data, err}
				return
			}
			data = append(data, buf[:count]...)
		}
		results <- readResult{data, nil}
	}()
	select {
	case result := <-results:
		if result.err != nil {
			t.Fatalf("%s: read failed after %q: %v", name, result.data, result.err)
		}
		return result.data
	case <-time.After(testTimeout):
		t.Fatalf("%s: timed out waiting for %d bytes", name, length)
		return nil
	}
}

func TestPTYCarriesBytesBothWaysUnchanged(t *testing.T) {
	port, appFD := openTestPort(t)
	appRead := func(buf []byte) (int, error) { return unix.Read(appFD, buf) }

	if _, err := unix.Write(appFD, []byte("AT\r\n")); err != nil {
		t.Fatalf("app write: %v", err)
	}
	if got := readExactly(t, "port", 4, port.Read); string(got) != "AT\r\n" {
		t.Fatalf("port read %q, want %q", got, "AT\r\n")
	}

	if _, err := port.Write([]byte("OK\r\n\x03\x00")); err != nil {
		t.Fatalf("port write: %v", err)
	}
	if got := readExactly(t, "app", 6, appRead); string(got) != "OK\r\n\x03\x00" {
		t.Fatalf("app read %q, want %q", got, "OK\r\n\x03\x00")
	}

	if _, err := unix.Write(appFD, []byte("next")); err != nil {
		t.Fatalf("app write: %v", err)
	}
	if got := readExactly(t, "port", 4, port.Read); string(got) != "next" {
		t.Fatalf("port read %q, want %q: data written to the port was echoed back", got, "next")
	}
}

func TestPTYSurvivesProgramReopening(t *testing.T) {
	port, appFD := openTestPort(t)
	unix.Close(appFD)

	secondFD, err := unix.Open(port.Path(), unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("reopen %s: %v", port.Path(), err)
	}
	defer unix.Close(secondFD)
	if _, err := unix.Write(secondFD, []byte("again")); err != nil {
		t.Fatalf("app write: %v", err)
	}
	if got := readExactly(t, "port", 5, port.Read); string(got) != "again" {
		t.Fatalf("port read %q, want %q", got, "again")
	}
}

func TestPTYLinkLifecycle(t *testing.T) {
	link := filepath.Join(t.TempDir(), "dev", "board")
	port, err := Open(Options{Path: link})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if port.Path() != link {
		t.Fatalf("Path %q, want %q", port.Path(), link)
	}
	target, err := os.Readlink(link)
	if err != nil || !strings.HasPrefix(target, "/dev/") {
		t.Fatalf("link target %q, err %v", target, err)
	}

	replacement, err := Open(Options{Path: link})
	if err != nil {
		t.Fatalf("Open over stale link: %v", err)
	}
	if err := port.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("closing the old port removed the link now owned by the new one: %v", err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("link still present after Close: %v", err)
	}
}

func TestPTYRefusesToReplaceRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	port, err := Open(Options{Path: path})
	if err == nil {
		port.Close()
		t.Fatal("Open replaced a regular file")
	}
	if content, readErr := os.ReadFile(path); readErr != nil || string(content) != "keep" {
		t.Fatalf("file changed: %q, %v", content, readErr)
	}
}

func TestPTYWithoutPathUsesSlaveName(t *testing.T) {
	port, err := Open(Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer port.Close()
	if !strings.HasPrefix(port.Path(), "/dev/") {
		t.Fatalf("Path %q is not a device path", port.Path())
	}
}

func TestPTYCloseUnblocksRead(t *testing.T) {
	port, _ := openTestPort(t)
	result := make(chan error, 1)
	go func() {
		_, err := port.Read(make([]byte, 16))
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("Read returned before Close: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := port.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("Read error %v, want os.ErrClosed", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Read still blocked after Close")
	}
	if _, err := port.Read(make([]byte, 16)); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Read after Close: %v, want os.ErrClosed", err)
	}
	if _, open := <-port.Events(); open {
		t.Fatal("Events not closed after Close")
	}
}

func TestPTYCloseUnblocksWrite(t *testing.T) {
	port, _ := openTestPort(t)
	result := make(chan error, 1)
	go func() {
		_, err := port.Write(make([]byte, 8<<20))
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("Write of 8 MiB returned with nobody reading: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := port.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("Write error %v, want os.ErrClosed", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Write still blocked after Close")
	}
}

func TestPTYReportsBaudRateChange(t *testing.T) {
	port, appFD := openTestPort(t)
	if !port.Capabilities().LineSettings {
		t.Fatal("pty should report line settings")
	}
	if err := setTestBaud(appFD, 115200); err != nil {
		t.Fatalf("set baud: %v", err)
	}
	select {
	case event := <-port.Events():
		if event.Kind != EventLineSettings || event.Line.BaudRate != 115200 || event.Line.DataBits != 8 {
			t.Fatalf("event %+v", event)
		}
	case <-time.After(testTimeout):
		t.Fatal("no event after baud rate change")
	}
}

func TestPTYQueuesSettingsChangeBeforeData(t *testing.T) {
	port, appFD := openTestPort(t)
	for _, baud := range []int{57600, 230400, 460800} {
		if err := setTestBaud(appFD, baud); err != nil {
			t.Fatalf("set baud: %v", err)
		}
		if _, err := unix.Write(appFD, []byte("x")); err != nil {
			t.Fatalf("app write: %v", err)
		}
		readExactly(t, "port", 1, port.Read)
		select {
		case event := <-port.Events():
			if event.Line.BaudRate != baud {
				t.Fatalf("event baud %d, want %d", event.Line.BaudRate, baud)
			}
		default:
			t.Fatalf("Read returned data before the change to %d baud was queued", baud)
		}
	}
}

func TestPTYModemStatusUnsupported(t *testing.T) {
	port, _ := openTestPort(t)
	if err := port.SetModemStatus(ModemStatus{CTS: true}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SetModemStatus error %v, want ErrUnsupported", err)
	}
}

func TestPTYConcurrentPortsRemainIndependent(t *testing.T) {
	firstPort, firstApp := openTestPort(t)
	secondPort, secondApp := openTestPort(t)
	if firstPort.Path() == secondPort.Path() {
		t.Fatal("independent ports share a device path")
	}
	if _, err := unix.Write(firstApp, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.Write(secondApp, []byte("second")); err != nil {
		t.Fatal(err)
	}
	if got := readExactly(t, "first port", 5, firstPort.Read); string(got) != "first" {
		t.Fatalf("first port received another session's data: %q", got)
	}
	if got := readExactly(t, "second port", 6, secondPort.Read); string(got) != "second" {
		t.Fatalf("second port received another session's data: %q", got)
	}
	if err := firstPort.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := secondPort.Write([]byte("live")); err != nil {
		t.Fatalf("closing first port affected second: %v", err)
	}
	if got := readExactly(t, "second app", 4, func(buffer []byte) (int, error) {
		return unix.Read(secondApp, buffer)
	}); string(got) != "live" {
		t.Fatalf("second app received %q", got)
	}
}
