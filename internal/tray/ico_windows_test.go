//go:build windows

package tray

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestParseICO(t *testing.T) {
	data := testICO([]testIconEntry{
		{width: 16, height: 16, bitCount: 8, data: []byte{1, 2, 3}},
		{width: 0, height: 0, bitCount: 32, data: []byte{4, 5, 6, 7}},
	})
	frames, err := parseICO(data)
	if err != nil {
		t.Fatalf("parseICO() error = %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("len(frames) = %d, want 2", len(frames))
	}
	if frames[0].width != 16 || frames[0].height != 16 || frames[0].bitCount != 8 {
		t.Fatalf("frames[0] = %+v", frames[0])
	}
	if frames[1].width != 256 || frames[1].height != 256 || frames[1].bitCount != 32 {
		t.Fatalf("frames[1] dimensions/depth = %dx%d/%d", frames[1].width, frames[1].height, frames[1].bitCount)
	}
}

func TestEmbeddedTrayICOParses(t *testing.T) {
	frames, err := parseICO(trayIconData)
	if err != nil {
		t.Fatalf("parseICO(trayIconData) error = %v", err)
	}
	if _, err := selectIconFrame(frames, 16, 16); err != nil {
		t.Fatalf("selectIconFrame(trayIconData) error = %v", err)
	}
	icon, err := createTrayIcon(16, 16)
	if err != nil {
		t.Fatalf("createTrayIcon() error = %v", err)
	}
	if icon == 0 {
		t.Fatal("createTrayIcon() returned an empty HICON")
	}
	destroyIcon.Call(icon)
}

func TestParseICORejectsCorruptData(t *testing.T) {
	valid := testICO([]testIconEntry{{width: 16, height: 16, bitCount: 32, data: []byte{1, 2, 3, 4}}})
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "short header", data: []byte{0, 0, 1}, want: "header"},
		{name: "wrong type", data: append([]byte(nil), valid...), want: "not a Windows icon"},
		{name: "empty", data: []byte{0, 0, 1, 0, 0, 0}, want: "no images"},
		{name: "truncated directory", data: []byte{0, 0, 1, 0, 1, 0}, want: "directory"},
		{name: "directory overlap", data: append([]byte(nil), valid...), want: "overlaps"},
		{name: "out of bounds", data: append([]byte(nil), valid...), want: "out of bounds"},
	}
	tests[1].data[2] = 2
	binary.LittleEndian.PutUint32(tests[4].data[18:22], 6)
	binary.LittleEndian.PutUint32(tests[5].data[14:18], 100)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseICO(test.data)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parseICO() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestSelectIconFrame(t *testing.T) {
	frames := []iconFrame{
		{width: 16, height: 16, bitCount: 32},
		{width: 32, height: 32, bitCount: 8},
		{width: 32, height: 32, bitCount: 32},
		{width: 48, height: 48, bitCount: 32},
	}
	selected, err := selectIconFrame(frames, 24, 24)
	if err != nil {
		t.Fatalf("selectIconFrame() error = %v", err)
	}
	if selected.width != 32 || selected.height != 32 || selected.bitCount != 32 {
		t.Fatalf("selected = %dx%d/%d, want 32x32/32", selected.width, selected.height, selected.bitCount)
	}
}

func TestQuitSignalOnlyClosesOnce(t *testing.T) {
	signal := newQuitSignal()
	signal.signal()
	signal.signal()
	select {
	case <-signal.channel():
	default:
		t.Fatal("quit channel is not closed")
	}
}

type testIconEntry struct {
	width    byte
	height   byte
	bitCount uint16
	data     []byte
}

func testICO(entries []testIconEntry) []byte {
	directoryEnd := icoHeaderSize + len(entries)*icoDirectorySize
	total := directoryEnd
	for _, entry := range entries {
		total += len(entry.data)
	}
	result := make([]byte, total)
	binary.LittleEndian.PutUint16(result[2:4], 1)
	binary.LittleEndian.PutUint16(result[4:6], uint16(len(entries)))
	dataOffset := directoryEnd
	for index, entry := range entries {
		offset := icoHeaderSize + index*icoDirectorySize
		result[offset] = entry.width
		result[offset+1] = entry.height
		binary.LittleEndian.PutUint16(result[offset+4:offset+6], 1)
		binary.LittleEndian.PutUint16(result[offset+6:offset+8], entry.bitCount)
		binary.LittleEndian.PutUint32(result[offset+8:offset+12], uint32(len(entry.data)))
		binary.LittleEndian.PutUint32(result[offset+12:offset+16], uint32(dataOffset))
		copy(result[dataOffset:], entry.data)
		dataOffset += len(entry.data)
	}
	return result
}
