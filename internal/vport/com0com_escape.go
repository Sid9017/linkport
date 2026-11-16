package vport

import "encoding/binary"

const (
	com0comSignature = "c0c\x00"

	serialLSRMSTEscape    = 0x00
	serialLSRMSTLSRData   = 0x01
	serialLSRMSTLSRNoData = 0x02
	serialLSRMSTMST       = 0x03
	com0comInsertRBR      = 16
	com0comInsertRLC      = 17

	com0comIoctlGet  = 0x01000000
	com0comEnableMST = 0x00000002
	com0comEnableRBR = 0x00000100
	com0comEnableRLC = 0x00000200

	modemStatusCTS = 0x10
	modemStatusDSR = 0x20
)

func com0comInsertRequest(escape byte, options uint32) []byte {
	request := make([]byte, 1+len(com0comSignature)+4)
	request[0] = escape
	copy(request[1:], com0comSignature)
	binary.LittleEndian.PutUint32(request[1+len(com0comSignature):], options)
	return request
}

func com0comPayloadLength(code byte) int {
	switch code {
	case serialLSRMSTLSRData:
		return 2
	case serialLSRMSTLSRNoData, serialLSRMSTMST:
		return 1
	case com0comInsertRBR:
		return 4
	case com0comInsertRLC:
		return 3
	default:
		return 0
	}
}

// com0comDecoder strips the insertions com0com adds to the data stream when
// extended IOCTL_SERIAL_LSRMST_INSERT is enabled. Sequences may be split
// across calls.
type com0comDecoder struct {
	escape   byte
	inEscape bool
	sequence []byte
}

// decode removes insertions from buf in place and returns how many data bytes
// remain at its start. Each completed insertion is passed to handle as its
// code and payload; the payload is only valid during the call.
func (decoder *com0comDecoder) decode(buf []byte, handle func(code byte, payload []byte)) int {
	dataLength := 0
	for _, value := range buf {
		if !decoder.inEscape {
			if value == decoder.escape {
				decoder.inEscape = true
				decoder.sequence = decoder.sequence[:0]
				continue
			}
			buf[dataLength] = value
			dataLength++
			continue
		}
		decoder.sequence = append(decoder.sequence, value)
		code := decoder.sequence[0]
		if len(decoder.sequence) < 1+com0comPayloadLength(code) {
			continue
		}
		decoder.inEscape = false
		switch code {
		case serialLSRMSTEscape:
			buf[dataLength] = decoder.escape
			dataLength++
		case serialLSRMSTLSRData:
			buf[dataLength] = decoder.sequence[2]
			dataLength++
		default:
			handle(code, decoder.sequence[1:])
		}
	}
	return dataLength
}

// com0comState turns insertions describing the paired port into events.
type com0comState struct {
	line    LineSettings
	control ControlLines
}

// apply records an insertion and reports the resulting event, if the
// insertion changed anything.
func (state *com0comState) apply(code byte, payload []byte) (Event, bool) {
	switch code {
	case serialLSRMSTMST:
		control := ControlLines{
			DTR: payload[0]&modemStatusDSR != 0,
			RTS: payload[0]&modemStatusCTS != 0,
		}
		if control == state.control {
			return Event{}, false
		}
		state.control = control
		return Event{Kind: EventControlLines, Control: control}, true
	case com0comInsertRBR:
		line := state.line
		line.BaudRate = int(binary.LittleEndian.Uint32(payload))
		return state.setLine(line)
	case com0comInsertRLC:
		line := state.line
		line.DataBits = int(payload[0])
		line.Parity = com0comParity(payload[1])
		line.StopBits = com0comStopBits(payload[2])
		return state.setLine(line)
	default:
		return Event{}, false
	}
}

func (state *com0comState) setLine(line LineSettings) (Event, bool) {
	if line == state.line {
		return Event{}, false
	}
	state.line = line
	return Event{Kind: EventLineSettings, Line: line}, true
}

func com0comParity(value byte) Parity {
	switch value {
	case 1:
		return ParityOdd
	case 2:
		return ParityEven
	case 3:
		return ParityMark
	case 4:
		return ParitySpace
	default:
		return ParityNone
	}
}

func com0comStopBits(value byte) StopBits {
	switch value {
	case 1:
		return StopBits1Half
	case 2:
		return StopBits2
	default:
		return StopBits1
	}
}
