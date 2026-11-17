package serialport

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

var ErrUnsupported = errors.ErrUnsupported

type Port struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	IsUSB        bool   `json:"is_usb"`
	VID          string `json:"vid,omitempty"`
	PID          string `json:"pid,omitempty"`
	SerialNumber string `json:"serial_number,omitempty"`
	Product      string `json:"product,omitempty"`
}

type Enumerator interface {
	Enumerate(context.Context) ([]Port, error)
}

type EnumerateFunc func(context.Context) ([]Port, error)

func (enumerate EnumerateFunc) Enumerate(ctx context.Context) ([]Port, error) {
	return enumerate(ctx)
}

type SystemEnumerator struct{}

func NewEnumerator() Enumerator {
	return SystemEnumerator{}
}

func (SystemEnumerator) Enumerate(ctx context.Context) ([]Port, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ports, err := enumerateSystem()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for index := range ports {
		ports[index] = Normalize(ports[index])
	}
	return ports, nil
}

func Normalize(port Port) Port {
	port.VID = normalizeUSBID(port.VID)
	port.PID = normalizeUSBID(port.PID)
	port.ID = Identity(port)
	return port
}

func Identity(port Port) string {
	vid := normalizeUSBID(port.VID)
	pid := normalizeUSBID(port.PID)
	if port.IsUSB && vid != "" && pid != "" && port.SerialNumber != "" {
		return "usb:" + vid + ":" + pid + ":" + url.PathEscape(port.SerialNumber)
	}
	return "path:" + url.PathEscape(port.Path)
}

func normalizeUSBID(value string) string {
	if value == "" {
		return ""
	}
	number, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(value), "0x"), 16, 16)
	if err != nil {
		return ""
	}
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[number>>12], digits[(number>>8)&15], digits[(number>>4)&15], digits[number&15]})
}
