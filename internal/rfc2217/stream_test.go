package rfc2217

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Sid9017/linkport/internal/vport"
)

func tcpPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); server.Close() })
	client.SetDeadline(time.Now().Add(5 * time.Second))
	server.SetDeadline(time.Now().Add(5 * time.Second))
	return client, server
}

func TestStreamEscapesBinaryAndHandlesFragmentedControl(t *testing.T) {
	clientConn, serverConn := tcpPair(t)
	stream := New(clientConn, nil, false)
	commands := make(chan []byte, 1)
	stream.CommandHandler = func(command byte, value []byte) error {
		if command == 107 {
			commands <- append([]byte(nil), value...)
		}
		return nil
	}
	expected := []byte{0, 255, 1, '\r', '\n', 128}
	go func() {
		for _, value := range []byte{
			iac, do, comOption,
			iac, sb, comOption, 107, iac, iac, iac, se,
			0, iac, iac, 1, '\r', '\n', 128,
		} {
			serverConn.Write([]byte{value})
		}
	}()
	if err := stream.Negotiate(); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(expected))
	if _, err := io.ReadFull(stream, got); err != nil || !bytes.Equal(got, expected) {
		t.Fatalf("received %x, error %v", got, err)
	}
	select {
	case command := <-commands:
		if !bytes.Equal(command, []byte{255}) {
			t.Fatalf("modem command %x", command)
		}
	default:
		t.Fatal("fragmented modem command lost")
	}
	if _, err := stream.Write(expected); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(serverConn)
	negotiation := make([]byte, 9)
	if _, err := io.ReadFull(reader, negotiation); err != nil {
		t.Fatal(err)
	}
	encoded := make([]byte, len(expected)+1)
	if _, err := io.ReadFull(reader, encoded); err != nil || !bytes.Equal(encoded, escaped(expected)) {
		t.Fatalf("written %x, error %v", encoded, err)
	}
}

func TestConfigureRequiresAcknowledgedActualSettings(t *testing.T) {
	clientConn, serverConn := tcpPair(t)
	client := New(clientConn, nil, false)
	server := New(serverConn, nil, true)
	server.CommandHandler = func(command byte, value []byte) error {
		if command == 2 {
			return server.Command(command+100, []byte{7})
		}
		return server.Command(command+100, value)
	}
	if err := client.Negotiate(); err != nil {
		t.Fatal(err)
	}
	if err := server.Negotiate(); err != nil {
		t.Fatal(err)
	}
	go io.Copy(io.Discard, client)
	go io.Copy(io.Discard, server)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Configure(ctx, vport.LineSettings{BaudRate: 115200, DataBits: 8}); err == nil {
		t.Fatal("accepted rejected data size")
	}
}
