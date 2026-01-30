//go:build windows

package passthrough

import (
	"os"
	"path/filepath"

	"github.com/winfsp/go-winfsp/gofs"
)

type Passthrough struct {
	Dir string
}

func (ptfs *Passthrough) OpenFile(name string, flag int, perm os.FileMode) (gofs.File, error) {
	return os.OpenFile(filepath.Join(ptfs.Dir, name), flag, perm)
}

func (ptfs *Passthrough) Mkdir(name string, perm os.FileMode) error {
	return os.Mkdir(filepath.Join(ptfs.Dir, name), perm)
}

func (ptfs *Passthrough) Remove(name string) error {
	return os.Remove(filepath.Join(ptfs.Dir, name))
}

func (ptfs *Passthrough) Rename(source string, target string) error {
	return os.Rename(filepath.Join(ptfs.Dir, source), filepath.Join(ptfs.Dir, target))
}

func (ptfs *Passthrough) Stat(name string) (os.FileInfo, error) {
	return os.Stat(filepath.Join(ptfs.Dir, name))
}

var _ gofs.FileSystem = (*Passthrough)(nil)
