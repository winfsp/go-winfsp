//go:build windows

package winfsp_test

import (
	"testing"
	"unsafe"

	"github.com/winfsp/go-winfsp"
)

// TestStructLayout checks struct layouts against winfsp/fsctl.h.
func TestStructLayout(t *testing.T) {
	var (
		fileInfo winfsp.FSP_FSCTL_FILE_INFO
		dirInfo  winfsp.FSP_FSCTL_DIR_INFO
		paramsV0 winfsp.FSP_FSCTL_VOLUME_PARAMS_V0
		paramsV1 winfsp.FSP_FSCTL_VOLUME_PARAMS_V1
	)
	for _, tc := range []struct {
		name      string
		got, want uintptr
	}{
		{"sizeof(FSP_FSCTL_FILE_INFO)", unsafe.Sizeof(fileInfo), 72},
		{"sizeof(FSP_FSCTL_DIR_INFO)", unsafe.Sizeof(dirInfo), 104},
		{"offsetof(FSP_FSCTL_DIR_INFO.FileInfo)", unsafe.Offsetof(dirInfo.FileInfo), 8},
		{"offsetof(FSP_FSCTL_DIR_INFO.NextOffset)", unsafe.Offsetof(dirInfo.NextOffset), 80},
		{"sizeof(FSP_FSCTL_VOLUME_PARAMS_V0)", unsafe.Sizeof(paramsV0), 456},
		{"sizeof(FSP_FSCTL_VOLUME_PARAMS_V1)", unsafe.Sizeof(paramsV1), 504},
		{"offsetof(FSP_FSCTL_VOLUME_PARAMS_V1.VolumeCreationTime)", unsafe.Offsetof(paramsV1.VolumeCreationTime), 8},
		{"offsetof(FSP_FSCTL_VOLUME_PARAMS_V1.Reserved64)", unsafe.Offsetof(paramsV1.Reserved64), 488},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}
