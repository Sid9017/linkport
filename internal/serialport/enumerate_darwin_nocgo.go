//go:build darwin && !cgo

package serialport

import "go.bug.st/serial"

func enumerateSystem() ([]Port, error) {
	paths, err := serial.GetPortsList()
	if err != nil {
		return nil, err
	}
	ports := make([]Port, 0, len(paths))
	for _, path := range paths {
		ports = append(ports, Port{Path: path})
	}
	return ports, nil
}
