package winfsp

import (
	"fmt"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/winfsp/go-winfsp/filetime"
)

// DebugStruct is the interface to signify that
// the struct has internal fields, which can be
// serialized by the .Field method.
//
// Please notice that the .Field method can return
// nil, and the caller must handle that.
type DebugStruct interface {
	Fields() map[string]any
}

// JoinDebugStructFields with comma.
func JoinDebugStructFields(s DebugStruct) string {
	m := s.Fields()
	if m == nil {
		return ""
	}
	var fields []string
	for key, value := range m {
		fields = append(fields, fmt.Sprintf("%s: %v", key, value))
	}
	return strings.Join(fields, ", ")
}

// DebugCreateOptions is the format wrapper for
// debugging `createOptions` flags.
type DebugCreateOptions uint32

var createOptionsNameMap = map[uint32]string{
	windows.FILE_DIRECTORY_FILE:            "DIRECTORY_FILE",
	windows.FILE_NON_DIRECTORY_FILE:        "NON_DIRECTORY_FILE",
	windows.FILE_WRITE_THROUGH:             "WRITE_THROUGH",
	windows.FILE_SEQUENTIAL_ONLY:           "SEQUENTIAL_ONLY",
	windows.FILE_RANDOM_ACCESS:             "RANDOM_ACCESS",
	windows.FILE_NO_INTERMEDIATE_BUFFERING: "NO_INTERMEDIATE_BUFFERING",
	windows.FILE_SYNCHRONOUS_IO_ALERT:      "SYNCHRONOUS_IO_ALERT",
	windows.FILE_SYNCHRONOUS_IO_NONALERT:   "SYNCHRONOUS_IO_NO_ALERT",
	windows.FILE_CREATE_TREE_CONNECTION:    "CREATE_TREE_CONNECTION",
	windows.FILE_NO_EA_KNOWLEDGE:           "NO_EA_KNOWLEDGE",
	windows.FILE_OPEN_REPARSE_POINT:        "OPEN_REPARSE_POINT",
	windows.FILE_DELETE_ON_CLOSE:           "DELETE_ON_CLOSE",
	windows.FILE_OPEN_BY_FILE_ID:           "OPEN_BY_FILE_ID",
	windows.FILE_OPEN_FOR_BACKUP_INTENT:    "OPEN_FOR_BACKUP_INTENT",
	windows.FILE_RESERVE_OPFILTER:          "RESERVE_OPFILTER",
	windows.FILE_OPEN_REQUIRING_OPLOCK:     "OPEN_REQUIRING_OPLOCK",
	windows.FILE_COMPLETE_IF_OPLOCKED:      "COMPLETE_IF_OPLOCKED",
}

func (d DebugCreateOptions) String() string {
	createOptions := uint32(d)
	var flags []string
	disposition := (createOptions >> 24) & 0x0ff
	knownDisposition := ""
	switch disposition {
	case windows.FILE_SUPERSEDE:
		knownDisposition = "SUPERSEDE"
	case windows.FILE_CREATE:
		knownDisposition = "CREATE"
	case windows.FILE_OPEN:
		knownDisposition = "OPEN"
	case windows.FILE_OPEN_IF:
		knownDisposition = "OPEN_IF"
	case windows.FILE_OVERWRITE:
		knownDisposition = "OVERWRITE"
	case windows.FILE_OVERWRITE_IF:
		knownDisposition = "OVERWRITE_IF"
	default:
	}
	if knownDisposition != "" {
		flags = append(flags, knownDisposition)
		createOptions ^= (disposition << 24)
	}
	for option, name := range createOptionsNameMap {
		if createOptions&option != 0 {
			flags = append(flags, name)
			createOptions ^= option
		}
	}
	if createOptions != 0 {
		flags = append(flags, fmt.Sprintf("0x%x", createOptions))
	}
	if len(flags) == 0 {
		return "0"
	}
	return strings.Join(flags, "|")
}

// DebugGrantedAccess is the format wrapper for
// debugging `grantedAccess` flags.
type DebugGrantedAccess uint32

var grantedAccessNameMap = map[uint32]string{
	windows.DELETE:                "DELETE",
	windows.FILE_READ_DATA:        "READ_DATA|LIST_DIRECTORY",
	windows.FILE_READ_ATTRIBUTES:  "READ_ATTRIBUTES",
	windows.FILE_READ_EA:          "READ_EA",
	windows.READ_CONTROL:          "READ_CONTROL",
	windows.FILE_WRITE_DATA:       "WRITE_DATA",
	windows.FILE_WRITE_ATTRIBUTES: "WRITE_ATTRIUBTES",
	windows.FILE_WRITE_EA:         "WRITE_EA",
	windows.FILE_APPEND_DATA:      "APPEND_DATA",
	windows.WRITE_DAC:             "WRITE_DAC",
	windows.WRITE_OWNER:           "WRITE_OWNER",
	windows.SYNCHRONIZE:           "SYNCHRONIZE",
	windows.FILE_EXECUTE:          "EXECUTE|TRAVERSE",
}

func (d DebugGrantedAccess) String() string {
	grantedAccess := uint32(d)
	var flags []string
	var genericAccess uint32
	if (grantedAccess & windows.GENERIC_READ) == windows.GENERIC_READ {
		flags = append(flags, "GENERIC_READ")
		genericAccess |= windows.GENERIC_READ
	}
	if (grantedAccess & windows.GENERIC_WRITE) == windows.GENERIC_WRITE {
		flags = append(flags, "GENERIC_WRITE")
		genericAccess |= windows.GENERIC_WRITE
	}
	if (grantedAccess & windows.GENERIC_EXECUTE) == windows.GENERIC_EXECUTE {
		flags = append(flags, "GENERIC_EXECUTE")
		genericAccess |= windows.GENERIC_EXECUTE
	}
	grantedAccess ^= genericAccess
	for option, name := range grantedAccessNameMap {
		if grantedAccess&option != 0 {
			flags = append(flags, name)
			grantedAccess ^= option
		}
	}
	if grantedAccess != 0 {
		flags = append(flags, fmt.Sprintf("0x%x", grantedAccess))
	}
	if len(flags) == 0 {
		return "0"
	}
	return strings.Join(flags, "|")
}

// DebugFileAttributes is the format wrapper
// for debugging `fileAttributes` flags.
type DebugFileAttributes uint32

// https://learn.microsoft.com/zh-tw/windows/win32/fileio/file-attribute-constants
var fileAttributesNameMap = map[uint32]string{
	windows.FILE_ATTRIBUTE_ARCHIVE:               "ARCHIVE",
	windows.FILE_ATTRIBUTE_COMPRESSED:            "COMPRESSED",
	windows.FILE_ATTRIBUTE_DEVICE:                "DEVICE",
	windows.FILE_ATTRIBUTE_DIRECTORY:             "DIRECTORY",
	windows.FILE_ATTRIBUTE_ENCRYPTED:             "ENCRYPTED",
	windows.FILE_ATTRIBUTE_INTEGRITY_STREAM:      "INTEGRITY_STREAM",
	windows.FILE_ATTRIBUTE_NORMAL:                "NORMAL",
	windows.FILE_ATTRIBUTE_NOT_CONTENT_INDEXED:   "NOT_CONTENT_INDEXED",
	windows.FILE_ATTRIBUTE_NO_SCRUB_DATA:         "NO_SCRUB_DATA",
	windows.FILE_ATTRIBUTE_OFFLINE:               "OFFLINE",
	windows.FILE_ATTRIBUTE_READONLY:              "READONLY",
	windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS: "RECALL_ON_DATA_ACCESS",
	windows.FILE_ATTRIBUTE_RECALL_ON_OPEN:        "RECALL_ON_OPEN",
	windows.FILE_ATTRIBUTE_REPARSE_POINT:         "REPARSE_POINT",
	windows.FILE_ATTRIBUTE_SPARSE_FILE:           "SPARSE_FILE",
	windows.FILE_ATTRIBUTE_SYSTEM:                "SYSTEM",
	windows.FILE_ATTRIBUTE_TEMPORARY:             "TEMPORARY",
	windows.FILE_ATTRIBUTE_VIRTUAL:               "VIRTUAL",
}

func (d DebugFileAttributes) String() string {
	fileAttributes := uint32(d)
	var flags []string
	for option, name := range fileAttributesNameMap {
		if fileAttributes&option != 0 {
			flags = append(flags, name)
			fileAttributes ^= option
		}
	}
	if fileAttributes != 0 {
		flags = append(flags, fmt.Sprintf("0x%x", fileAttributes))
	}
	if len(flags) == 0 {
		return "0"
	}
	return strings.Join(flags, "|")
}

// DebugFiletime is the format wrapper for
// debugging syscall.Filetime / uint64 data.
type DebugFiletime uint64

func (d DebugFiletime) String() string {
	fileTime := uint64(d)
	return fmt.Sprintf(
		"syscall.Filetime(%d, %q)",
		fileTime,
		filetime.TimeFromRaw(fileTime).Format(time.RFC3339),
	)
}

// DebugFileInfo is the format wrapper for
// debugging *FSP_FSCTL_FILE_INFO struct.
type DebugFileInfo struct {
	*FSP_FSCTL_FILE_INFO
}

func (d DebugFileInfo) Fields() map[string]any {
	fileInfo := d.FSP_FSCTL_FILE_INFO
	if fileInfo == nil {
		return nil
	}
	return map[string]any{
		"FileAttributes": DebugFileAttributes(fileInfo.FileAttributes),
		"ReparseTag":     fileInfo.ReparseTag,
		"AllocationSize": fileInfo.AllocationSize,
		"FileSize":       fileInfo.FileSize,
		"CreationTime":   DebugFiletime(fileInfo.CreationTime),
		"LastAccessTime": DebugFiletime(fileInfo.LastAccessTime),
		"LastWriteTime":  DebugFiletime(fileInfo.LastWriteTime),
		"ChangeTime":     DebugFiletime(fileInfo.ChangeTime),
		"IndexNumber":    fileInfo.IndexNumber,
		"HardLinks":      fileInfo.HardLinks,
		"EaSize":         fileInfo.EaSize,
	}
}

func (d DebugFileInfo) String() string {
	if d.FSP_FSCTL_FILE_INFO == nil {
		return "(*FSP_FSCTL_FILE_INFO)(nil)"
	}
	return "&FSP_FSCTL_FILE_INFO{ " + JoinDebugStructFields(d) + " }"
}

// DebugVolumeInfo is the format wrapper for
// debugging *FSP_FSCTL_VOLUME_INFO struct.
type DebugVolumeInfo struct {
	*FSP_FSCTL_VOLUME_INFO
}

func (d DebugVolumeInfo) Fields() map[string]any {
	volumeInfo := d.FSP_FSCTL_VOLUME_INFO
	if volumeInfo == nil {
		return nil
	}
	var volumeLabel []uint16
	labelLen := min(int(d.VolumeLabelLength), len(d.VolumeLabel[:]))
	volumeLabel = append(volumeLabel, d.VolumeLabel[:labelLen]...)
	volumeLabel = append(volumeLabel, 0)
	label := syscall.UTF16ToString(volumeLabel)

	return map[string]any{
		"TotalSize":         volumeInfo.TotalSize,
		"FreeSize":          volumeInfo.FreeSize,
		"VolumeLabelLength": volumeInfo.VolumeLabelLength,
		"VolumeLabel":       label,
	}
}

func (d DebugVolumeInfo) String() string {
	if d.FSP_FSCTL_VOLUME_INFO == nil {
		return "(*FSP_FSCTL_VOLUME_INFO)(nil)"
	}
	return "&FSP_FSCTL_VOLUME_INFO{ " + JoinDebugStructFields(d) + " }"
}
