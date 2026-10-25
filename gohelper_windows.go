package winfsp

import (
	"unicode/utf16"
	"unsafe"
)

// This file is even more restrictive than the
// "helper_windows.go", it stores functions that
// is marked as "helper" in "winfsp/winfsp.h" and
// implemented by go for some reason.
const ()

const (
	dirInfoAlignment uint16 = 8        // FSP_FSCTL_DEFAULT_ALIGNMENT
	replacementChar         = '\uFFFD' // Unicode replacement character
)

// FileSystemAddDirInfo adds directory information to a buffer like
// FspFileSystemAddDirInfo.
//
// The buffer must be aligned to FSP_FSCTL_DIR_INFO, e.g. the buf
// parameter of ReadDirectoryRaw.
func FileSystemAddDirInfo(
	name string,
	nextOffset uint64,
	fileInfo *FSP_FSCTL_FILE_INFO,
	buffer []byte,
) int {
	if fileInfo == nil {
		// Then we just need to write two null bytes.
		if len(buffer) < 2 {
			return 0
		}
		buffer[0] = 0
		buffer[1] = 0
		return 2
	}

	var utf16Len uint16
	for _, r := range name {
		switch utf16.RuneLen(r) {
		case 1:
			utf16Len++
		case 2:
			utf16Len += 2
		default:
			utf16Len++
		}
	}

	dirInfoSize := uint16(unsafe.Sizeof(FSP_FSCTL_DIR_INFO{}))
	requiredSize := dirInfoSize + utf16Len*SIZEOF_WCHAR
	alignedSize := (requiredSize + dirInfoAlignment - 1) & ^(dirInfoAlignment - 1)
	if uint16(len(buffer)) < alignedSize {
		return 0
	}

	di := (*FSP_FSCTL_DIR_INFO)(unsafe.Pointer(&buffer[0]))
	di.FileInfo = *fileInfo
	di.NextOffset = nextOffset
	di.Padding0 = 0
	di.Padding1 = 0
	di.Size = requiredSize

	// Encode the string directly into the buffer as UTF-16
	var utf16Buffer []uint16 = unsafe.Slice((*uint16)(unsafe.Pointer(&buffer[dirInfoSize])), utf16Len)
	utf16Index := 0
	for _, r := range name {
		switch utf16.RuneLen(r) {
		case 1:
			utf16Buffer[utf16Index] = uint16(r)
			utf16Index++
		case 2:
			r1, r2 := utf16.EncodeRune(r)
			utf16Buffer[utf16Index] = uint16(r1)
			utf16Buffer[utf16Index+1] = uint16(r2)
			utf16Index += 2
		default:
			utf16Buffer[utf16Index] = uint16(replacementChar)
			utf16Index++
		}
	}

	return int(alignedSize)
}

// streamInfoHeaderSize is FSP_FSCTL_STREAM_INFO's fixed part: Size
// (UINT16, padded to 8), StreamSize and StreamAllocationSize (UINT64
// each), followed by the WCHAR name.
const streamInfoHeaderSize = 24

// FileSystemAddStreamInfo adds one stream's information to a
// GetStreamInfo buffer like FspFileSystemAddStreamInfo: name is the
// stream name without the leading colon or ":$DATA" ("" = the main,
// unnamed stream). It returns the bytes written, or 0 if the entry does
// not fit. Finish the buffer with FileSystemAddStreamInfoEnd.
func FileSystemAddStreamInfo(name string, size, allocationSize uint64, buffer []byte) int {
	encoded := utf16.Encode([]rune(name))
	requiredSize := streamInfoHeaderSize + len(encoded)*SIZEOF_WCHAR
	alignedSize := (requiredSize + int(dirInfoAlignment) - 1) &^ (int(dirInfoAlignment) - 1)
	if len(buffer) < alignedSize {
		return 0
	}
	*(*uint16)(unsafe.Pointer(&buffer[0])) = uint16(requiredSize)
	for i := 2; i < 8; i++ {
		buffer[i] = 0
	}
	*(*uint64)(unsafe.Pointer(&buffer[8])) = size
	*(*uint64)(unsafe.Pointer(&buffer[16])) = allocationSize
	for i, c := range encoded {
		*(*uint16)(unsafe.Pointer(&buffer[streamInfoHeaderSize+i*SIZEOF_WCHAR])) = c
	}
	return alignedSize
}

// FileSystemAddStreamInfoEnd terminates a GetStreamInfo buffer, like
// FspFileSystemAddStreamInfo with a NULL StreamInfo. It returns the
// bytes written, or 0 if there is no room.
func FileSystemAddStreamInfoEnd(buffer []byte) int {
	if len(buffer) < 2 {
		return 0
	}
	buffer[0], buffer[1] = 0, 0
	return 2
}
