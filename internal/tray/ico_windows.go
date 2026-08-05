//go:build windows

package tray

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	icoHeaderSize    = 6
	icoDirectorySize = 16
)

type iconFrame struct {
	width    int
	height   int
	bitCount uint16
	data     []byte
}

func parseICO(data []byte) ([]iconFrame, error) {
	if len(data) < icoHeaderSize {
		return nil, errors.New("ICO header is truncated")
	}
	if binary.LittleEndian.Uint16(data[0:2]) != 0 {
		return nil, errors.New("ICO reserved field is not zero")
	}
	if binary.LittleEndian.Uint16(data[2:4]) != 1 {
		return nil, errors.New("resource is not a Windows icon")
	}

	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 {
		return nil, errors.New("ICO contains no images")
	}
	directoryEnd := uint64(icoHeaderSize) + uint64(count)*icoDirectorySize
	if directoryEnd > uint64(len(data)) {
		return nil, errors.New("ICO directory is truncated")
	}

	frames := make([]iconFrame, 0, count)
	for index := 0; index < count; index++ {
		entryOffset := icoHeaderSize + index*icoDirectorySize
		entry := data[entryOffset : entryOffset+icoDirectorySize]
		width := int(entry[0])
		if width == 0 {
			width = 256
		}
		height := int(entry[1])
		if height == 0 {
			height = 256
		}
		size := uint64(binary.LittleEndian.Uint32(entry[8:12]))
		offset := uint64(binary.LittleEndian.Uint32(entry[12:16]))
		if size == 0 {
			return nil, fmt.Errorf("ICO image %d is empty", index)
		}
		if offset < directoryEnd {
			return nil, fmt.Errorf("ICO image %d overlaps the directory", index)
		}
		if offset > uint64(len(data)) || size > uint64(len(data))-offset {
			return nil, fmt.Errorf("ICO image %d data is out of bounds", index)
		}
		frames = append(frames, iconFrame{
			width:    width,
			height:   height,
			bitCount: binary.LittleEndian.Uint16(entry[6:8]),
			data:     data[int(offset):int(offset+size)],
		})
	}
	return frames, nil
}

func selectIconFrame(frames []iconFrame, targetWidth, targetHeight int) (iconFrame, error) {
	if len(frames) == 0 {
		return iconFrame{}, errors.New("ICO contains no selectable images")
	}
	if targetWidth <= 0 || targetHeight <= 0 {
		return iconFrame{}, errors.New("target icon size must be positive")
	}

	best := frames[0]
	for _, candidate := range frames[1:] {
		if iconFrameBetter(candidate, best, targetWidth, targetHeight) {
			best = candidate
		}
	}
	return best, nil
}

func iconFrameBetter(candidate, current iconFrame, targetWidth, targetHeight int) bool {
	candidateDistance := iconDistance(candidate, targetWidth, targetHeight)
	currentDistance := iconDistance(current, targetWidth, targetHeight)
	if candidateDistance != currentDistance {
		return candidateDistance < currentDistance
	}
	candidateUndersized := candidate.width < targetWidth || candidate.height < targetHeight
	currentUndersized := current.width < targetWidth || current.height < targetHeight
	if candidateUndersized != currentUndersized {
		return !candidateUndersized
	}
	if candidate.bitCount != current.bitCount {
		return candidate.bitCount > current.bitCount
	}
	return candidate.width*candidate.height > current.width*current.height
}

func iconDistance(frame iconFrame, targetWidth, targetHeight int) int64 {
	dx := int64(frame.width - targetWidth)
	dy := int64(frame.height - targetHeight)
	return dx*dx + dy*dy
}
