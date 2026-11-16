package rfc2217

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Sid9017/linkport/internal/vport"
)

const (
	iac          = 255
	sb           = 250
	se           = 240
	will         = 251
	wont         = 252
	do           = 253
	dont         = 254
	binaryOption = 0
	comOption    = 44
)

type Stream struct {
	net.Conn
	reader         *bufio.Reader
	server         bool
	writeMu        sync.Mutex
	configMu       sync.Mutex
	sentWill       [256]bool
	sentDo         [256]bool
	ready          chan struct{}
	readyOnce      sync.Once
	acks           chan acknowledgment
	CommandHandler func(byte, []byte) error
}

type acknowledgment struct {
	command byte
	value   []byte
}

func New(conn net.Conn, reader *bufio.Reader, server bool) *Stream {
	if reader == nil {
		reader = bufio.NewReader(conn)
	}
	return &Stream{Conn: conn, reader: reader, server: server, ready: make(chan struct{}), acks: make(chan acknowledgment, 32)}
}

func (stream *Stream) Negotiate() error {
	stream.sentWill[binaryOption], stream.sentDo[binaryOption] = true, true
	packet := []byte{iac, will, binaryOption, iac, do, binaryOption}
	if stream.server {
		stream.sentDo[comOption] = true
		packet = append(packet, iac, do, comOption)
	} else {
		stream.sentWill[comOption] = true
		packet = append(packet, iac, will, comOption)
	}
	return stream.packet(packet)
}

func (stream *Stream) WaitReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-stream.ready:
		return nil
	}
}

func (stream *Stream) packet(packet []byte) error {
	stream.writeMu.Lock()
	defer stream.writeMu.Unlock()
	if err := stream.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	defer stream.Conn.SetWriteDeadline(time.Time{})
	for len(packet) != 0 {
		count, err := stream.Conn.Write(packet)
		if err != nil {
			return err
		}
		if count == 0 {
			return io.ErrShortWrite
		}
		packet = packet[count:]
	}
	return nil
}

func escaped(value []byte) []byte {
	return bytes.ReplaceAll(value, []byte{iac}, []byte{iac, iac})
}

func (stream *Stream) Write(content []byte) (int, error) {
	if err := stream.packet(escaped(content)); err != nil {
		return 0, err
	}
	return len(content), nil
}

func (stream *Stream) Command(command byte, value []byte) error {
	packet := []byte{iac, sb, comOption, command}
	packet = append(packet, escaped(value)...)
	packet = append(packet, iac, se)
	return stream.packet(packet)
}

func (stream *Stream) Read(content []byte) (int, error) {
	if len(content) == 0 {
		return 0, nil
	}
	count := 0
	for count < len(content) {
		value, err := stream.reader.ReadByte()
		if err != nil {
			return count, err
		}
		if value != iac {
			content[count] = value
			count++
		} else {
			command, err := stream.reader.ReadByte()
			if err != nil {
				return count, err
			}
			switch command {
			case iac:
				content[count] = iac
				count++
			case will, wont, do, dont:
				option, err := stream.reader.ReadByte()
				if err != nil {
					return count, err
				}
				if err := stream.negotiate(command, option); err != nil {
					return count, err
				}
			case sb:
				if err := stream.subnegotiation(); err != nil {
					return count, err
				}
			}
		}
		if count > 0 && stream.reader.Buffered() == 0 {
			return count, nil
		}
	}
	return count, nil
}

func (stream *Stream) negotiate(command, option byte) error {
	switch command {
	case do:
		if option == binaryOption || (option == comOption && !stream.server) {
			if !stream.sentWill[option] {
				stream.sentWill[option] = true
				if err := stream.packet([]byte{iac, will, option}); err != nil {
					return err
				}
			}
			if option == comOption {
				stream.readyOnce.Do(func() { close(stream.ready) })
			}
		} else {
			return stream.packet([]byte{iac, wont, option})
		}
	case will:
		if option == binaryOption || (option == comOption && stream.server) {
			if !stream.sentDo[option] {
				stream.sentDo[option] = true
				if err := stream.packet([]byte{iac, do, option}); err != nil {
					return err
				}
			}
			if option == comOption {
				stream.readyOnce.Do(func() { close(stream.ready) })
			}
		} else {
			return stream.packet([]byte{iac, dont, option})
		}
	case wont, dont:
		if option == comOption || option == binaryOption {
			return errors.New("remote rejected RFC 2217 binary serial negotiation")
		}
	}
	return nil
}

func (stream *Stream) subnegotiation() error {
	packet := make([]byte, 0, 16)
	for len(packet) <= 4096 {
		value, err := stream.reader.ReadByte()
		if err != nil {
			return err
		}
		if value == iac {
			value, err = stream.reader.ReadByte()
			if err != nil {
				return err
			}
			if value == se {
				if len(packet) < 2 || packet[0] != comOption {
					return nil
				}
				command, payload := packet[1], packet[2:]
				if !stream.server && command >= 101 && command <= 105 {
					select {
					case stream.acks <- acknowledgment{command: command - 100, value: append([]byte(nil), payload...)}:
					default:
					}
				}
				if stream.CommandHandler != nil {
					return stream.CommandHandler(command, payload)
				}
				return nil
			}
			if value != iac {
				return errors.New("invalid Telnet subnegotiation")
			}
		}
		packet = append(packet, value)
	}
	return errors.New("RFC 2217 command exceeds size limit")
}

func Validate(settings vport.LineSettings) error {
	if settings.BaudRate <= 0 || settings.BaudRate > 4000000 || settings.DataBits < 5 || settings.DataBits > 8 ||
		settings.Parity < vport.ParityNone || settings.Parity > vport.ParitySpace ||
		settings.StopBits < vport.StopBits1 || settings.StopBits > vport.StopBits2 {
		return errors.New("invalid serial settings")
	}
	return nil
}

func (stream *Stream) request(ctx context.Context, command byte, value []byte) error {
	if err := stream.Command(command, value); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ack := <-stream.acks:
			if ack.command != command {
				continue
			}
			if !bytes.Equal(ack.value, value) {
				return fmt.Errorf("remote rejected serial setting %d", command)
			}
			return nil
		}
	}
}

func (stream *Stream) Configure(ctx context.Context, settings vport.LineSettings) error {
	if err := Validate(settings); err != nil {
		return err
	}
	stream.configMu.Lock()
	defer stream.configMu.Unlock()
	baud := make([]byte, 4)
	binary.BigEndian.PutUint32(baud, uint32(settings.BaudRate))
	stop := byte(1)
	if settings.StopBits == vport.StopBits2 {
		stop = 2
	} else if settings.StopBits == vport.StopBits1Half {
		stop = 3
	}
	for _, setting := range []acknowledgment{
		{1, baud}, {2, []byte{byte(settings.DataBits)}},
		{3, []byte{byte(settings.Parity) + 1}}, {4, []byte{stop}},
	} {
		if err := stream.request(ctx, setting.command, setting.value); err != nil {
			return err
		}
	}
	return nil
}

func (stream *Stream) SetControl(ctx context.Context, control vport.ControlLines) error {
	stream.configMu.Lock()
	defer stream.configMu.Unlock()
	dtr, rts := byte(9), byte(12)
	if control.DTR {
		dtr = 8
	}
	if control.RTS {
		rts = 11
	}
	if err := stream.request(ctx, 5, []byte{dtr}); err != nil {
		return err
	}
	return stream.request(ctx, 5, []byte{rts})
}
