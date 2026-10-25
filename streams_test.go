//go:build windows

package winfsp_test

import (
	"bytes"
	"encoding/binary"
	"syscall"
	"testing"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/winfsp/go-winfsp"
	"github.com/winfsp/go-winfsp/gofs"
)

// TestFileSystemAddStreamInfo checks the entries FileSystemAddStreamInfo
// writes against FSP_FSCTL_STREAM_INFO in winfsp/fsctl.h: Size (UINT16),
// then StreamSize and StreamAllocationSize (UINT64) at offsets 8 and 16,
// then the name, the whole entry padded to 8 bytes.
func TestFileSystemAddStreamInfo(t *testing.T) {
	buf := bytes.Repeat([]byte{0xff}, 64)
	n := winfsp.FileSystemAddStreamInfo("ads", 5, 4096, buf)
	if want := 24 + 3*2; int(binary.LittleEndian.Uint16(buf)) != want {
		t.Errorf("Size = %d, want %d", binary.LittleEndian.Uint16(buf), want)
	}
	if n != 32 {
		t.Errorf("returned %d, want 32 (30 bytes padded to 8)", n)
	}
	if got := binary.LittleEndian.Uint64(buf[8:]); got != 5 {
		t.Errorf("StreamSize = %d, want 5", got)
	}
	if got := binary.LittleEndian.Uint64(buf[16:]); got != 4096 {
		t.Errorf("StreamAllocationSize = %d, want 4096", got)
	}
	name := make([]uint16, 3)
	for i := range name {
		name[i] = binary.LittleEndian.Uint16(buf[24+2*i:])
	}
	if got := string(utf16.Decode(name)); got != "ads" {
		t.Errorf("StreamNameBuf = %q, want %q", got, "ads")
	}

	if n := winfsp.FileSystemAddStreamInfo("ads", 5, 4096, buf[:31]); n != 0 {
		t.Errorf("into 31 bytes: returned %d, want 0 (doesn't fit)", n)
	}
	if n := winfsp.FileSystemAddStreamInfoEnd(buf[:2]); n != 2 || buf[0] != 0 || buf[1] != 0 {
		t.Errorf("end marker: returned %d, buffer %v", n, buf[:2])
	}
	if n := winfsp.FileSystemAddStreamInfoEnd(buf[:1]); n != 0 {
		t.Errorf("end marker into 1 byte: returned %d, want 0", n)
	}
}

// gofsBehaviours is what gofs's file system implements, so a test can
// add a behaviour to it by embedding.
type gofsBehaviours interface {
	winfsp.BehaviourBase
	winfsp.BehaviourGetSecurityByName
	winfsp.BehaviourCreate
	winfsp.BehaviourOverwrite
	winfsp.BehaviourCleanup
	winfsp.BehaviourRead
	winfsp.BehaviourWrite
	winfsp.BehaviourFlush
	winfsp.BehaviourGetFileInfo
	winfsp.BehaviourSetBasicInfo
	winfsp.BehaviourSetFileSize
	winfsp.BehaviourCanDelete
	winfsp.BehaviourRename
	winfsp.BehaviourGetSecurity
	winfsp.BehaviourGetVolumeInfo
	winfsp.BehaviourSetVolumeLabel
	winfsp.BehaviourReadDirectory
	winfsp.BehaviourDefaultOptions
}

// streamFS is a gofs file system whose every file reports its main
// stream and one named stream, "ads", of 5 bytes.
type streamFS struct {
	gofsBehaviours
}

func (streamFS) GetStreamInfo(fs *winfsp.FileSystemRef, file uintptr, buf []byte) (int, error) {
	n := 0
	for _, s := range []struct {
		name string
		size uint64
	}{{"", uint64(len(helloWorld))}, {"ads", 5}} {
		w := winfsp.FileSystemAddStreamInfo(s.name, s.size, s.size, buf[n:])
		if w == 0 {
			return n, nil
		}
		n += w
	}
	return n + winfsp.FileSystemAddStreamInfoEnd(buf[n:]), nil
}

var (
	modkernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procFindFirstStreamW = modkernel32.NewProc("FindFirstStreamW")
	procFindNextStreamW  = modkernel32.NewProc("FindNextStreamW")
)

// win32FindStreamData is WIN32_FIND_STREAM_DATA.
type win32FindStreamData struct {
	StreamSize int64
	StreamName [windows.MAX_PATH + 36]uint16
}

// streams lists a file's streams as dir /r and PowerShell's
// Get-Item -Stream do, through FindFirstStreamW and FindNextStreamW.
func streams(t *testing.T, path string) map[string]int64 {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	var data win32FindStreamData
	h, _, err := procFindFirstStreamW.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&data)), 0)
	if windows.Handle(h) == windows.InvalidHandle {
		t.Fatalf("FindFirstStreamW(%q): %v", path, err)
	}
	defer windows.FindClose(windows.Handle(h))
	got := map[string]int64{}
	for {
		got[windows.UTF16ToString(data.StreamName[:])] = data.StreamSize
		if ok, _, err := procFindNextStreamW.Call(h, uintptr(unsafe.Pointer(&data))); ok == 0 {
			if err != syscall.Errno(windows.ERROR_HANDLE_EOF) {
				t.Fatalf("FindNextStreamW(%q): %v", path, err)
			}
			return got
		}
	}
}

// TestGetStreamInfo mounts a file system implementing
// BehaviourGetStreamInfo and lists a file's streams as Windows tools do.
func TestGetStreamInfo(t *testing.T) {
	testFS := newTestFS()
	testFS.addTestFile(`\hello.txt`, []byte(helloWorld))
	fspFS, err := winfsp.Mount(streamFS{gofs.New(testFS).(gofsBehaviours)}, "W:")
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	defer fspFS.Unmount()

	got := streams(t, `W:\hello.txt`)
	want := map[string]int64{"::$DATA": int64(len(helloWorld)), ":ads:$DATA": 5}
	if len(got) != len(want) {
		t.Fatalf("streams = %v, want %v", got, want)
	}
	for name, size := range want {
		if got[name] != size {
			t.Errorf("streams = %v, want %v", got, want)
		}
	}
}
