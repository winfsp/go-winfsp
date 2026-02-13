//go:build windows

package memfs

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"github.com/winfsp/go-winfsp/gofs"
)

type memObject interface {
	size() int64
}

type memFile struct {
	dataMtx sync.Mutex
	// Must acquire data.dataMtx to modify.
	data []byte
}

func (m *memFile) size() int64 {
	return int64(len(m.data))
}

var _ memObject = (*memFile)(nil)

type memDir struct {
	// Must acquire memfs.fsMtx to modify.
	dentries map[string]*memItem
}

func (m *memDir) size() int64 {
	return int64(0)
}

var _ memObject = (*memDir)(nil)

type memItem struct {
	metaMtx    sync.Mutex
	name       string
	mode       os.FileMode
	createTime time.Time
	accessTime time.Time
	modifyTime time.Time
	obj        memObject
}

func newMemItem(mode os.FileMode, name string, obj memObject) *memItem {
	now := time.Now()
	return &memItem{
		name:       name,
		mode:       mode,
		createTime: now,
		accessTime: now,
		modifyTime: now,
		obj:        obj,
	}
}

func (m *memItem) touch() {
	m.metaMtx.Lock()
	defer m.metaMtx.Unlock()
	now := time.Now()
	m.accessTime = now
	m.modifyTime = now
}

type memStat struct {
	name       string
	mode       os.FileMode
	modifyTime time.Time
	size       int64
}

func (s memStat) IsDir() bool        { return s.mode.IsDir() }
func (s memStat) ModTime() time.Time { return s.modifyTime }
func (s memStat) Mode() fs.FileMode  { return s.mode }
func (s memStat) Name() string       { return s.name }
func (s memStat) Size() int64        { return s.size }
func (memStat) Sys() any             { return nil }

var _ os.FileInfo = memStat{}

func (item *memItem) stat() os.FileInfo {
	item.metaMtx.Lock()
	defer item.metaMtx.Unlock()
	return memStat{
		name:       item.name,
		mode:       item.mode,
		modifyTime: item.modifyTime,
		size:       item.obj.size(),
	}
}

type MemFS struct {
	mtx      sync.Mutex
	rootItem *memItem
	rootDir  *memDir
}

func New() *MemFS {
	rootDir := &memDir{
		dentries: make(map[string]*memItem),
	}
	rootItem := newMemItem(
		os.FileMode(0777)|os.ModeDir,
		"\\", rootDir,
	)
	result := &MemFS{
		rootItem: rootItem,
		rootDir:  rootDir,
	}
	return result
}

type memOpenFile struct {
	item   *memItem
	flag   int
	file   *memFile
	offset int64
}

func (m *memOpenFile) Close() error               { return nil }
func (m *memOpenFile) Stat() (os.FileInfo, error) { return m.item.stat(), nil }

func (m *memOpenFile) Sync() error {
	m.item.touch()
	return nil
}

const (
	allModeFlags = os.O_RDONLY | os.O_WRONLY | os.O_RDWR
)

func (m *memOpenFile) Read(p []byte) (n int, err error) {
	numRead, err := m.ReadAt(p, m.offset)
	m.offset += int64(numRead)
	return numRead, err
}

func (m *memOpenFile) ReadAt(p []byte, off int64) (n int, err error) {
	if m.flag&allModeFlags == os.O_WRONLY {
		return 0, windows.ERROR_ACCESS_DENIED
	}

	defer m.item.touch()
	m.file.dataMtx.Lock()
	defer m.file.dataMtx.Unlock()
	sliceOff := min(off, int64(len(m.file.data)))
	numRead := copy(p, m.file.data[sliceOff:])
	if numRead == 0 && len(p) > 0 {
		return 0, io.EOF
	}
	return numRead, nil
}

func (m *memOpenFile) Readdir(count int) ([]os.FileInfo, error) {
	return nil, windows.STATUS_NOT_A_DIRECTORY
}

func (m *memOpenFile) Seek(offset int64, whence int) (int64, error) {
	m.file.dataMtx.Lock()
	defer m.file.dataMtx.Unlock()
	switch whence {
	case io.SeekStart:
		m.offset = 0
	case io.SeekEnd:
		m.offset = int64(len(m.file.data))
	}
	m.offset += offset
	return m.offset, nil
}

func (file *memFile) reserveLocked(size int64) {
	lesser := size - int64(len(file.data))
	if lesser > 0 {
		filling := make([]byte, int(lesser))
		file.data = append(file.data, filling...)
	}
}

func (m *memOpenFile) Truncate(size int64) error {
	defer m.item.touch()
	m.file.dataMtx.Lock()
	defer m.file.dataMtx.Unlock()
	m.file.reserveLocked(size)
	m.file.data = m.file.data[:size]
	return nil
}

func (m *memOpenFile) writeAtLocked(p []byte, off int64) (n int, err error) {
	sliceOff := min(off, int64(len(m.file.data)))
	numWritten := copy(m.file.data[sliceOff:], p)
	return numWritten, nil
}

func (m *memOpenFile) writeWithDataLock(f func() (int, error)) (int, error) {
	if m.flag&allModeFlags == os.O_RDONLY {
		return 0, windows.STATUS_ACCESS_DENIED
	}
	defer m.item.touch()
	m.file.dataMtx.Lock()
	defer m.file.dataMtx.Unlock()
	return f()
}

func (m *memOpenFile) Write(p []byte) (n int, err error) {
	return m.writeWithDataLock(func() (int, error) {
		numWritten, err := m.writeAtLocked(p, m.offset)
		m.offset += int64(numWritten)
		return numWritten, err
	})
}

func (m *memOpenFile) WriteAt(p []byte, off int64) (n int, err error) {
	return m.writeWithDataLock(func() (int, error) {
		if m.flag&os.O_APPEND != 0 {
			return 0, windows.STATUS_ACCESS_DENIED
		}
		m.file.reserveLocked(off + int64(len(p)))
		return m.writeAtLocked(p, off)
	})
}

var _ gofs.File = (*memOpenFile)(nil)

func (m *memOpenFile) Append(buf []byte) (int, error) {
	return m.writeWithDataLock(func() (int, error) {
		m.file.data = append(m.file.data, buf...)
		return len(buf), nil
	})
}

func (m *memOpenFile) ConstrainedWriteAt(p []byte, off int64) (int, error) {
	return m.writeWithDataLock(func() (int, error) {
		return m.writeAtLocked(p, off)
	})
}

var _ gofs.FileWriteEx = (*memOpenFile)(nil)

func (m *memOpenFile) Shrink(newSize int64) error {
	defer m.item.touch()
	m.file.dataMtx.Lock()
	defer m.file.dataMtx.Unlock()
	if newSize < int64(len(m.file.data)) {
		m.file.data = m.file.data[:newSize]
	}
	return nil
}

var _ gofs.FileTruncateEx = (*memOpenFile)(nil)

type memOpenDir struct {
	fs       *MemFS
	item     *memItem
	dir      *memDir
	snapOnce sync.Once
	snapshot []os.FileInfo
	off      int64
}

const (
	errIsDir = windows.STATUS_FILE_IS_A_DIRECTORY
)

func (m *memOpenDir) Close() error                                   { return nil }
func (m *memOpenDir) Read(p []byte) (n int, err error)               { return 0, errIsDir }
func (m *memOpenDir) ReadAt(p []byte, off int64) (n int, err error)  { return 0, errIsDir }
func (m *memOpenDir) Seek(offset int64, whence int) (int64, error)   { return 0, errIsDir }
func (m *memOpenDir) Truncate(size int64) error                      { return errIsDir }
func (m *memOpenDir) Write(p []byte) (n int, err error)              { return 0, errIsDir }
func (m *memOpenDir) WriteAt(p []byte, off int64) (n int, err error) { return 0, errIsDir }

func (m *memOpenDir) Readdir(count int) ([]os.FileInfo, error) {
	m.snapOnce.Do(func() {
		m.fs.mtx.Lock()
		defer m.fs.mtx.Unlock()
		var names []string
		for name := range m.dir.dentries {
			names = append(names, name)
		}
		sort.Strings(names)
		var snapshot []os.FileInfo
		for _, name := range names {
			stat := m.dir.dentries[name].stat()
			snapshot = append(snapshot, stat)
		}
		m.snapshot = snapshot
		m.off = 0
	})
	var result []os.FileInfo
	if count <= 0 {
		result = append(result, m.snapshot...)
		return result, nil
	} else {
		result = make([]os.FileInfo, count)
		sliceOff := min(m.off, int64(len(m.snapshot)))
		copied := copy(result, m.snapshot[sliceOff:])
		if copied == 0 {
			return nil, io.EOF
		}
		return result[:copied], nil
	}
}

func (m *memOpenDir) Stat() (os.FileInfo, error) {
	return m.item.stat(), nil
}

func (m *memOpenDir) Sync() error {
	m.item.touch()
	return nil
}

var _ gofs.File = (*memOpenDir)(nil)

func (fs *MemFS) findDirLocked(path string) (*memItem, *memDir, error) {
	if path == "" || path == "\\" {
		return fs.rootItem, fs.rootDir, nil
	}
	var err error
	parentPath, base := filepath.Split(path)
	parentPath = filepath.Clean(parentPath)
	_, parentDir, err := fs.findDirLocked(parentPath)
	if err != nil {
		return nil, nil, err
	}
	item, ok := parentDir.dentries[base]
	if !ok {
		return nil, nil, os.ErrNotExist
	}
	dir, isDir := item.obj.(*memDir)
	if !isDir {
		return nil, nil, windows.STATUS_NOT_A_DIRECTORY
	}
	return item, dir, nil
}

func (m *MemFS) OpenFile(name string, flag int, perm os.FileMode) (gofs.File, error) {
	if name == "" || name == "\\" {
		return &memOpenDir{
			fs:   m,
			item: m.rootItem,
			dir:  m.rootDir,
		}, nil
	}

	m.mtx.Lock()
	defer m.mtx.Unlock()

	dirPath, base := filepath.Split(name)
	dirPath = filepath.Clean(dirPath)
	dirItem, dir, err := m.findDirLocked(dirPath)
	if err != nil {
		return nil, err
	}

	var result gofs.File
	if item, ok := dir.dentries[base]; ok {
		switch t := item.obj.(type) {
		case *memFile:
			result = &memOpenFile{
				item: item,
				flag: flag,
				file: t,
			}
		case *memDir:
			result = &memOpenDir{
				fs:   m,
				item: item,
				dir:  t,
			}
		default:
			return nil, windows.ERROR_ACCESS_DENIED
		}
	}

	const createExclFlags = os.O_CREATE | os.O_EXCL
	if result != nil && flag&createExclFlags == createExclFlags {
		return nil, os.ErrExist
	}

	if flag&os.O_CREATE != 0 && result == nil {
		file := &memFile{}
		item := newMemItem(perm.Perm(), base, file)
		dir.dentries[base] = item
		result = &memOpenFile{
			item: item,
			flag: flag,
			file: file,
		}
		dirItem.touch()
	}

	if result == nil {
		return nil, os.ErrNotExist
	}

	if flag&os.O_TRUNC != 0 {
		_ = result.Truncate(0)
	}

	return result, nil
}

func (m *MemFS) Mkdir(name string, perm os.FileMode) error {
	if name == "" || name == "\\" {
		return os.ErrExist
	}

	m.mtx.Lock()
	defer m.mtx.Unlock()

	dirPath, base := filepath.Split(name)
	dirPath = filepath.Clean(dirPath)
	dirItem, dir, err := m.findDirLocked(dirPath)
	if err != nil {
		return err
	}
	if _, ok := dir.dentries[base]; ok {
		return os.ErrExist
	}

	dir.dentries[base] = newMemItem(
		perm.Perm()|fs.ModeDir,
		base,
		&memDir{
			dentries: make(map[string]*memItem),
		},
	)
	dirItem.touch()
	return nil
}

func (m *MemFS) Remove(name string) error {
	if name == "\\" || name == "" {
		// Cannot delete root directory.
		return windows.STATUS_ACCESS_DENIED
	}

	m.mtx.Lock()
	defer m.mtx.Unlock()

	dirPath, base := filepath.Split(name)
	dirPath = filepath.Clean(dirPath)
	dirItem, dir, err := m.findDirLocked(dirPath)
	if err != nil {
		return err
	}
	item, ok := dir.dentries[base]
	if !ok {
		return os.ErrNotExist
	}

	switch obj := item.obj.(type) {
	case *memFile:
	case *memDir:
		if len(obj.dentries) > 0 {
			return windows.STATUS_DIRECTORY_NOT_EMPTY
		}
	default:
		return windows.STATUS_ACCESS_DENIED
	}

	delete(dir.dentries, base)
	dirItem.touch()
	return nil
}

func (m *MemFS) Rename(src string, tgt string) error {
	if src == "\\" || src == "" {
		return windows.STATUS_ACCESS_DENIED
	}
	if tgt == "\\" || tgt == "" {
		return windows.STATUS_ACCESS_DENIED
	}

	m.mtx.Lock()
	defer m.mtx.Unlock()

	srcDirPath, srcBase := filepath.Split(src)
	srcDirPath = filepath.Clean(srcDirPath)
	srcItem, srcDir, err := m.findDirLocked(srcDirPath)
	if err != nil {
		return err
	}
	item, ok := srcDir.dentries[srcBase]
	if !ok {
		return os.ErrNotExist
	}

	if (srcItem.mode.Perm() & 0200) == 0 {
		return windows.STATUS_ACCESS_DENIED
	}

	tgtDirPath, tgtBase := filepath.Split(tgt)
	tgtDirPath = filepath.Clean(tgtDirPath)
	tgtItem, tgtDir, err := m.findDirLocked(tgtDirPath)
	if err != nil {
		return err
	}

	if (tgtItem.mode.Perm() & 0200) == 0 {
		return windows.STATUS_ACCESS_DENIED
	}

	// Now it's safe to modify the file.
	delete(srcDir.dentries, srcBase)
	srcItem.touch()
	tgtDir.dentries[tgtBase] = item
	tgtItem.touch()
	func() {
		item.metaMtx.Lock()
		defer item.metaMtx.Unlock()
		item.name = tgtBase
	}()
	item.touch()
	return nil
}

func (m *MemFS) Stat(name string) (os.FileInfo, error) {
	if name == "" || name == "\\" {
		return m.rootItem.stat(), nil
	}
	m.mtx.Lock()
	defer m.mtx.Unlock()

	dirPath, base := filepath.Split(name)
	dirPath = filepath.Clean(dirPath)
	_, dir, err := m.findDirLocked(dirPath)
	if err != nil {
		return nil, err
	}
	item, ok := dir.dentries[base]
	if !ok {
		return nil, os.ErrNotExist
	}

	return item.stat(), nil
}

var _ gofs.FileSystem = (*MemFS)(nil)
