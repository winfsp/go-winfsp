//go:build windows

package winfsp_test

import (
	"testing"
	"unicode/utf16"
	"unsafe"

	"github.com/winfsp/go-winfsp"
)

// TestOpenFileInfoLayout checks FSP_FSCTL_OPEN_FILE_INFO against
// winfsp/fsctl.h: FileInfo, then PWSTR NormalizedName, then UINT16
// NormalizedNameSize.
func TestOpenFileInfoLayout(t *testing.T) {
	var info winfsp.FSP_FSCTL_OPEN_FILE_INFO
	ptr := unsafe.Sizeof(uintptr(0))
	if got := unsafe.Offsetof(info.NormalizedName); got != 72 {
		t.Errorf("offsetof(NormalizedName) = %d, want 72", got)
	}
	if got := unsafe.Offsetof(info.NormalizedNameSize); got != 72+ptr {
		t.Errorf("offsetof(NormalizedNameSize) = %d, want %d", got, 72+ptr)
	}
}

// TestSetNormalizedName: the name is copied into WinFsp's buffer and its
// size in bytes reported; a name that doesn't fit, or no buffer, sets
// nothing.
func TestSetNormalizedName(t *testing.T) {
	buf := make([]uint16, 16)
	info := winfsp.FSP_FSCTL_OPEN_FILE_INFO{NormalizedName: &buf[0], NormalizedNameSize: uint16(len(buf) * 2)}
	open := winfsp.GetOpenFileInfo(&info.FileInfo)
	if open != &info {
		t.Fatal("GetOpenFileInfo doesn't point at the FSP_FSCTL_OPEN_FILE_INFO")
	}
	if !open.SetNormalizedName(`\Dir\File.txt`) {
		t.Fatal("SetNormalizedName refused a name that fits")
	}
	if info.NormalizedNameSize != 13*2 {
		t.Errorf("NormalizedNameSize = %d, want %d", info.NormalizedNameSize, 13*2)
	}
	if got := string(utf16.Decode(buf[:13])); got != `\Dir\File.txt` {
		t.Errorf("NormalizedName = %q", got)
	}

	info.NormalizedNameSize = 8
	if open.SetNormalizedName(`\Dir\File.txt`) || info.NormalizedNameSize != 8 {
		t.Errorf("a name too long for the buffer: set, size %d", info.NormalizedNameSize)
	}
	if (&winfsp.FSP_FSCTL_OPEN_FILE_INFO{}).SetNormalizedName(`\a`) {
		t.Error("set a name with no buffer")
	}
}
