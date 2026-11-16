package rfc2217

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/Sid9017/linkport/internal/vport"
	"go.bug.st/serial"
)

func Mode(settings vport.LineSettings) *serial.Mode {
	stop := serial.OneStopBit
	if settings.StopBits == vport.StopBits2 {
		stop = serial.TwoStopBits
	} else if settings.StopBits == vport.StopBits1Half {
		stop = serial.OnePointFiveStopBits
	}
	return &serial.Mode{
		BaudRate: settings.BaudRate, DataBits: settings.DataBits,
		Parity: serial.Parity(settings.Parity), StopBits: stop,
		InitialStatusBits: &serial.ModemOutputBits{DTR: false, RTS: false},
	}
}

func Serve(ctx context.Context, stream *Stream, port serial.Port, initial vport.LineSettings) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			cancel()
			stream.Close()
			port.Close()
		})
	}
	defer closeAll()
	if err := port.SetReadTimeout(250 * time.Millisecond); err != nil {
		return err
	}
	current := initial
	control := vport.ControlLines{}
	var modemMu sync.Mutex
	modemMask := byte(0)
	stream.CommandHandler = func(command byte, payload []byte) error {
		next := current
		switch command {
		case 0:
			if len(payload) == 0 {
				return stream.Command(100, []byte("Linkport RFC 2217"))
			}
			return nil
		case 1:
			if len(payload) != 4 {
				return errors.New("invalid baud command")
			}
			if value := binary.BigEndian.Uint32(payload); value != 0 {
				next.BaudRate = int(value)
			}
		case 2, 3, 4:
			if len(payload) != 1 {
				return errors.New("invalid framing command")
			}
			if payload[0] != 0 {
				switch command {
				case 2:
					next.DataBits = int(payload[0])
				case 3:
					next.Parity = vport.Parity(payload[0] - 1)
				case 4:
					switch payload[0] {
					case 1:
						next.StopBits = vport.StopBits1
					case 2:
						next.StopBits = vport.StopBits2
					case 3:
						next.StopBits = vport.StopBits1Half
					default:
						next.StopBits = vport.StopBits(-1)
					}
				}
			}
		case 5:
			if len(payload) != 1 {
				return errors.New("invalid control command")
			}
			value := payload[0]
			switch value {
			case 7, 8, 9:
				if value != 7 {
					if err := port.SetDTR(value == 8); err != nil {
						return err
					}
					control.DTR = value == 8
				}
				value = 9
				if control.DTR {
					value = 8
				}
			case 10, 11, 12:
				if value != 10 {
					if err := port.SetRTS(value == 11); err != nil {
						return err
					}
					control.RTS = value == 11
				}
				value = 12
				if control.RTS {
					value = 11
				}
			case 4, 5, 6:
				value = 6
			case 13, 14, 15, 16, 18:
				value = 14
			default:
				value = 1
			}
			return stream.Command(105, []byte{value})
		case 10:
			if len(payload) != 1 {
				return errors.New("invalid line mask")
			}
			return stream.Command(110, []byte{0})
		case 11:
			if len(payload) != 1 {
				return errors.New("invalid modem mask")
			}
			modemMu.Lock()
			modemMask = payload[0]
			modemMu.Unlock()
			return stream.Command(111, payload)
		case 12:
			if len(payload) != 1 || payload[0] < 1 || payload[0] > 3 {
				return errors.New("invalid purge command")
			}
			if payload[0]&1 != 0 {
				if err := port.ResetInputBuffer(); err != nil {
					return err
				}
			}
			if payload[0]&2 != 0 {
				if err := port.ResetOutputBuffer(); err != nil {
					return err
				}
			}
			return stream.Command(112, payload)
		default:
			return nil
		}
		if next != current && Validate(next) == nil {
			if err := port.SetMode(Mode(next)); err == nil {
				current = next
			}
		}
		var response []byte
		switch command {
		case 1:
			response = make([]byte, 4)
			binary.BigEndian.PutUint32(response, uint32(current.BaudRate))
		case 2:
			response = []byte{byte(current.DataBits)}
		case 3:
			response = []byte{byte(current.Parity) + 1}
		case 4:
			value := byte(1)
			if current.StopBits == vport.StopBits2 {
				value = 2
			}
			if current.StopBits == vport.StopBits1Half {
				value = 3
			}
			response = []byte{value}
		}
		return stream.Command(command+100, response)
	}
	if err := stream.Negotiate(); err != nil {
		return err
	}
	finished := make(chan error, 3)
	go func() {
		_, err := io.Copy(port, stream)
		finished <- err
		closeAll()
	}()
	go func() {
		buffer := make([]byte, 4096)
		for ctx.Err() == nil {
			count, err := port.Read(buffer)
			if count > 0 {
				if _, writeErr := stream.Write(buffer[:count]); writeErr != nil {
					err = writeErr
				}
			}
			if err != nil {
				finished <- err
				closeAll()
				return
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		previous := byte(0)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				modemMu.Lock()
				mask := modemMask
				modemMu.Unlock()
				if mask == 0 {
					continue
				}
				bits, err := port.GetModemStatusBits()
				if err != nil {
					continue
				}
				value := byte(0)
				if bits.CTS {
					value |= 16
				}
				if bits.DSR {
					value |= 32
				}
				if bits.RI {
					value |= 64
				}
				if bits.DCD {
					value |= 128
				}
				delta := (value ^ previous) >> 4
				if value != previous {
					if err := stream.Command(107, []byte{(value | delta) & mask}); err != nil {
						finished <- err
						closeAll()
						return
					}
					previous = value
				}
			}
		}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-finished:
		return err
	}
}
