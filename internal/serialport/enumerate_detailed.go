//go:build (darwin && cgo) || linux || windows || freebsd || openbsd

package serialport

import "go.bug.st/serial/enumerator"

func enumerateSystem() ([]Port, error) {
	details, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, err
	}
	ports := make([]Port, 0, len(details))
	for _, detail := range details {
		if detail == nil {
			continue
		}
		ports = append(ports, Port{
			Path:         detail.Name,
			IsUSB:        detail.IsUSB,
			VID:          detail.VID,
			PID:          detail.PID,
			SerialNumber: detail.SerialNumber,
			Product:      detail.Product,
		})
	}
	return ports, nil
}
