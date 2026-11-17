package vport

import (
	"bytes"
	"testing"
)

const testEscape = 0x8e

type recordedInsertion struct {
	code    byte
	payload []byte
}

func decodeChunks(chunks ...[]byte) ([]byte, []recordedInsertion) {
	decoder := com0comDecoder{escape: testEscape}
	var data []byte
	var insertions []recordedInsertion
	for _, chunk := range chunks {
		buf := append([]byte(nil), chunk...)
		length := decoder.decode(buf, func(code byte, payload []byte) {
			insertions = append(insertions, recordedInsertion{code, append([]byte(nil), payload...)})
		})
		data = append(data, buf[:length]...)
	}
	return data, insertions
}

func TestCom0comDecoderPassesPlainData(t *testing.T) {
	data, insertions := decodeChunks([]byte("boot ok\r\n"))
	if string(data) != "boot ok\r\n" || len(insertions) != 0 {
		t.Fatalf("data %q, insertions %v", data, insertions)
	}
}

func TestCom0comDecoderUnescapesEscapeByte(t *testing.T) {
	data, insertions := decodeChunks([]byte{'a', testEscape, serialLSRMSTEscape, 'b'})
	if !bytes.Equal(data, []byte{'a', testEscape, 'b'}) || len(insertions) != 0 {
		t.Fatalf("data %v, insertions %v", data, insertions)
	}
}

func TestCom0comDecoderExtractsInsertions(t *testing.T) {
	stream := []byte{
		'a',
		testEscape, serialLSRMSTMST, 0x30,
		'b',
		testEscape, com0comInsertRBR, 0x00, 0xc2, 0x01, 0x00,
		testEscape, com0comInsertRLC, 7, 2, 2,
		testEscape, serialLSRMSTLSRNoData, 0x60,
		testEscape, serialLSRMSTLSRData, 0x10, 'c',
		'd',
	}
	want := []recordedInsertion{
		{serialLSRMSTMST, []byte{0x30}},
		{com0comInsertRBR, []byte{0x00, 0xc2, 0x01, 0x00}},
		{com0comInsertRLC, []byte{7, 2, 2}},
		{serialLSRMSTLSRNoData, []byte{0x60}},
	}

	check := func(name string, data []byte, insertions []recordedInsertion) {
		t.Helper()
		if string(data) != "abcd" {
			t.Errorf("%s: data %q, want %q", name, data, "abcd")
		}
		if len(insertions) != len(want) {
			t.Fatalf("%s: %d insertions, want %d", name, len(insertions), len(want))
		}
		for index, insertion := range insertions {
			if insertion.code != want[index].code || !bytes.Equal(insertion.payload, want[index].payload) {
				t.Errorf("%s: insertion %d is %v, want %v", name, index, insertion, want[index])
			}
		}
	}

	data, insertions := decodeChunks(stream)
	check("whole", data, insertions)

	var single [][]byte
	for index := range stream {
		single = append(single, stream[index:index+1])
	}
	data, insertions = decodeChunks(single...)
	check("byte by byte", data, insertions)
}

func TestCom0comStateReportsOnlyChanges(t *testing.T) {
	var state com0comState

	event, changed := state.apply(serialLSRMSTMST, []byte{modemStatusDSR})
	if !changed || event.Kind != EventControlLines || event.Control != (ControlLines{DTR: true}) {
		t.Fatalf("DSR high: %+v changed=%v", event, changed)
	}
	if _, changed := state.apply(serialLSRMSTMST, []byte{modemStatusDSR | 0x02}); changed {
		t.Fatal("repeated modem status reported as a change")
	}
	event, changed = state.apply(serialLSRMSTMST, []byte{modemStatusCTS})
	if !changed || event.Control != (ControlLines{RTS: true}) {
		t.Fatalf("CTS high: %+v changed=%v", event, changed)
	}

	event, changed = state.apply(com0comInsertRBR, []byte{0x00, 0xc2, 0x01, 0x00})
	if !changed || event.Kind != EventLineSettings || event.Line.BaudRate != 115200 {
		t.Fatalf("baud rate: %+v changed=%v", event, changed)
	}
	if _, changed := state.apply(com0comInsertRBR, []byte{0x00, 0xc2, 0x01, 0x00}); changed {
		t.Fatal("repeated baud rate reported as a change")
	}
	event, changed = state.apply(com0comInsertRLC, []byte{7, 2, 2})
	want := LineSettings{BaudRate: 115200, DataBits: 7, Parity: ParityEven, StopBits: StopBits2}
	if !changed || event.Line != want {
		t.Fatalf("line control: %+v changed=%v, want %+v", event, changed, want)
	}
}

func TestCom0comInsertRequestLayout(t *testing.T) {
	request := com0comInsertRequest(testEscape, com0comIoctlGet|com0comEnableMST|com0comEnableRBR|com0comEnableRLC)
	want := []byte{testEscape, 'c', '0', 'c', 0, 0x02, 0x03, 0x00, 0x01}
	if !bytes.Equal(request, want) {
		t.Fatalf("request %v, want %v", request, want)
	}
}
