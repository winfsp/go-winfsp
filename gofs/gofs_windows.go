package gofs

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/pkg/errors"
	"golang.org/x/sys/windows"

	"github.com/winfsp/go-winfsp"
	"github.com/winfsp/go-winfsp/filetime"
	"github.com/winfsp/go-winfsp/procsd"
	"github.com/winfsp/go-winfsp/treelock"
)

type File interface {
	io.ReadWriteCloser
	io.ReaderAt
	io.WriterAt
	io.Seeker

	Readdir(count int) ([]os.FileInfo, error)
	Stat() (os.FileInfo, error)
	Sync() error
	Truncate(size int64) error
}

var _ File = (*os.File)(nil)

type FileSystem interface {
	OpenFile(name string, flag int, perm os.FileMode) (File, error)
	Mkdir(name string, perm os.FileMode) error
	Stat(name string) (os.FileInfo, error)
	Rename(source, target string) error
	Remove(name string) error
}

type fileHandle struct {
	node  *treelock.Node
	dir   winfsp.DirBuffer
	file  File
	flags int
	mtx   sync.RWMutex

	evaluatedIndex uint64
}

// AttribReadOnlyTransMode controls how gofs
// translate the `FILE_ATTRIBUTE_READONLY`
// flag of a file.
//
// The `FILE_ATTRIBUTE_NORMAL` flag is not
// applicable to a directory, according to
// the Windows API. Therefore gofs will always
// clear this bit when it is a directory.
//
// Please notice while setting the read-only
// flag could make the file non-writable and
// non-deletable, it's all due to the Windows
// file system and WinFSP, and gofs performs
// **no** permission check internally. Therefore,
// one must **never** rely on gofs to grate
// unauthorized accesses, it's the duty for
// for `gofs.File` and `gofs.FileSystem`.
//
// The default value is `AttribReadOnlyWindows`.
type AttribReadOnlyTransMode uint64

const (
	// AttribReadOnlyWindows make gofs translate
	// the read-only attribute based on the
	// Windows semantics.
	//
	// That is, when `os.FileInfo.Mode()` has no
	// writable bit set for the current user,
	// it is viewed as read-only, rendering the
	// file as not writable to and deletable.
	// This is the way go os package translate
	// the permission bit on Windows platform.
	AttribReadOnlyWindows AttribReadOnlyTransMode = 0

	// AttribReadOnlyBypass make gofs clear
	// the read-only attribute indefinitely.
	AttribReadOnlyBypass AttribReadOnlyTransMode = 1

	// AttribReadOnlyAlways make gofs set
	// the read-only attribute indefinitely.
	AttribReadOnlyAlways AttribReadOnlyTransMode = 2

	// AttribReadOnlyPOSIX make gofs translate
	// the read-only attribute based on the
	// POSIX semantics.
	//
	// The translation is based on `os.FileInfo.Mode()`
	// of the current file and its parent directory.
	// The bit is set only if both the file
	// and the parent directory has no writable
	// bit set for the current user.
	AttribReadOnlyPOSIX AttribReadOnlyTransMode = 3

	AttribReadOnlyAllStyleBits = AttribReadOnlyTransMode(0) |
		AttribReadOnlyWindows |
		AttribReadOnlyBypass |
		AttribReadOnlyAlways |
		AttribReadOnlyPOSIX

	// AttribReadOnlyHonorSys make gofs honor
	// the `FileAttributes` field when `os.FileInfo.Sys()`
	// is `syscall.Win32FileAttributeData`.
	//
	// If `os.FileInfo.Sys()` is nil or not a
	// `syscall.Win32FileAttributeData`, it we will
	// fallback to the handling logic due to
	// `AttribReadOnlyAllStyleBits`.
	AttribReadOnlyHonorSys AttribReadOnlyTransMode = 4

	AttribReadOnlyAllBits = AttribReadOnlyTransMode(0) |
		AttribReadOnlyAllStyleBits |
		AttribReadOnlyHonorSys
)

type exiledParentStat struct{}

func (e *exiledParentStat) IsDir() bool        { return true }
func (e *exiledParentStat) ModTime() time.Time { return time.Now() }
func (e *exiledParentStat) Name() string       { return "" }
func (e *exiledParentStat) Size() int64        { return 0 }
func (e *exiledParentStat) Sys() any           { return nil }

func (e *exiledParentStat) Mode() fs.FileMode {
	// XXX: Once the node is exiled, it will be placed
	// under a pseudo parent directory whose content
	// cannot be deleted. This prevents deleting the
	// file twice.
	return os.FileMode(0o000)
}

var _ os.FileInfo = &exiledParentStat{}

type fileSystem struct {
	inner   FileSystem
	handles sync.Map
	locker  *treelock.TreeLocker

	labelLen int
	label    [32]uint16

	readOnlyTransMode AttribReadOnlyTransMode
}

func (fs *fileSystem) readOnlyBitFromSelfParentStats(
	selfStat, parentStat os.FileInfo,
) uint32 {
	if (fs.readOnlyTransMode & AttribReadOnlyHonorSys) != 0 {
		var attribData *syscall.Win32FileAttributeData
		if sys := selfStat.Sys(); sys != nil {
			if v, ok := sys.(*syscall.Win32FileAttributeData); ok {
				attribData = v
			}
		}
		if attribData != nil {
			return attribData.FileAttributes & windows.FILE_ATTRIBUTE_READONLY
		}
	}
	mode := selfStat.Mode()
	switch fs.readOnlyTransMode & AttribReadOnlyAllStyleBits {
	case AttribReadOnlyBypass:
		return 0
	case AttribReadOnlyAlways:
		return windows.FILE_ATTRIBUTE_READONLY
	case AttribReadOnlyPOSIX:
		selfWritable := (uint32(mode.Perm()) & 0200) != 0
		parentMode := os.FileMode(0)
		if parentStat != nil {
			parentMode = parentStat.Mode()
		}
		parentWritable := (uint32(parentMode.Perm()) & 0200) != 0
		if selfWritable || parentWritable {
			return 0
		}
		return windows.FILE_ATTRIBUTE_READONLY
	case AttribReadOnlyWindows:
		fallthrough
	default:
		if (uint32(mode.Perm()) & 0200) != 0 {
			return 0
		}
		return windows.FILE_ATTRIBUTE_READONLY
	}
}

func (fs *fileSystem) attributesFromSelfParentStats(
	selfStat, parentStat os.FileInfo,
) uint32 {
	mode := selfStat.Mode()
	var attributes uint32
	if mode.IsDir() {
		attributes |= windows.FILE_ATTRIBUTE_DIRECTORY
	} else if mode.IsRegular() {
		attributes |= fs.readOnlyBitFromSelfParentStats(selfStat, parentStat)
	}
	if attributes == 0 {
		attributes = windows.FILE_ATTRIBUTE_NORMAL
	}
	return attributes
}

func (fs *fileSystem) fillInfoFromSelfParentStats(
	target *winfsp.FSP_FSCTL_FILE_INFO,
	selfStat, parentStat os.FileInfo,
	evaluatedIndexNumber uint64,
) {
	target.FileAttributes = fs.attributesFromSelfParentStats(selfStat, parentStat)
	target.ReparseTag = 0
	target.FileSize = uint64(selfStat.Size())
	target.AllocationSize = ((target.FileSize + 4095) / 4096) * 4096
	target.CreationTime = filetime.Timestamp(selfStat.ModTime())
	target.LastAccessTime = target.CreationTime
	target.LastWriteTime = target.CreationTime
	target.ChangeTime = target.LastWriteTime
	target.IndexNumber = evaluatedIndexNumber
	target.HardLinks = 0
	target.EaSize = 0

	// We can extract more data from it if it is find data from
	// windows, which is the one from golang's standard library.
	sys := selfStat.Sys()
	if sys == nil {
		return
	}
	findData, ok := sys.(*syscall.Win32FileAttributeData)
	if !ok {
		return
	}
	target.CreationTime = filetime.Filetime(findData.CreationTime)
	target.LastAccessTime = filetime.Filetime(findData.LastAccessTime)
	target.LastWriteTime = filetime.Filetime(findData.LastWriteTime)
	target.ChangeTime = target.LastWriteTime
}

func (fs *fileSystem) needParentStat() bool {
	switch fs.readOnlyTransMode & AttribReadOnlyAllStyleBits {
	case AttribReadOnlyPOSIX:
		return true
	}
	return false
}

func (fs *fileSystem) fillInfoFromPathLocked(
	target *winfsp.FSP_FSCTL_FILE_INFO,
	path string,
	selfStat, parentStat os.FileInfo,
	evaluatedIndexNumber uint64,
) error {
	var err error
	if selfStat == nil {
		if selfStat, err = fs.inner.Stat(path); err != nil {
			return err
		}
	}
	if parentStat == nil && fs.needParentStat() {
		parent := filepath.Dir(treelock.UnifyFilePath(path))
		parent = treelock.UnifyFilePath(parent)
		if parentStat, err = fs.inner.Stat(parent); err != nil {
			return err
		}
	}
	fs.fillInfoFromSelfParentStats(
		target, selfStat, parentStat, evaluatedIndexNumber,
	)
	return nil
}

// fillInfoFromHandleLocked fills the information
// with a fileHandle. Must acquire the lock.
func (fs *fileSystem) fillInfoFromHandleLocked(
	target *winfsp.FSP_FSCTL_FILE_INFO,
	handle *fileHandle,
	selfStat, parentStat os.FileInfo,
) error {
	var err error
	if selfStat == nil && handle.file != nil {
		selfStat, err = handle.file.Stat()
		if err != nil {
			return err
		}
	}
	if parentStat == nil && fs.needParentStat() {
		if handle.node.IsExile() {
			parentStat = &exiledParentStat{}
		} else {
			parent := filepath.Dir(handle.node.FilePath())
			parent = treelock.UnifyFilePath(parent)
			if parentStat, err = fs.inner.Stat(parent); err != nil {
				return err
			}
		}
	}
	fs.fillInfoFromSelfParentStats(
		target, selfStat, parentStat, handle.evaluatedIndex,
	)
	return nil
}

// fillInfoFromHandle fills the information with
// a fileHandle. Will lock if the parent needs stat.
func (fs *fileSystem) fillInfoFromHandle(
	target *winfsp.FSP_FSCTL_FILE_INFO,
	handle *fileHandle,
	selfStat, parentStat os.FileInfo,
) error {
	if parentStat == nil && fs.needParentStat() {
		plock := handle.node.RLockPath()
		defer plock.Unlock()
	}
	return fs.fillInfoFromHandleLocked(
		target, handle, selfStat, parentStat,
	)
}

func (fs *fileSystem) GetSecurityByName(
	ref *winfsp.FileSystemRef, name string,
	flags winfsp.GetSecurityByNameFlags,
) (uint32, *windows.SECURITY_DESCRIPTOR, error) {
	var err error
	plock := fs.locker.RLockFile(name)
	defer plock.Unlock()
	name = plock.FilePath()
	info, err := fs.inner.Stat(name)
	if err != nil || flags == winfsp.GetExistenceOnly {
		return 0, nil, err
	}
	target := &winfsp.FSP_FSCTL_FILE_INFO{}
	err = fs.fillInfoFromPathLocked(target, name, info, nil, 0)
	if err != nil || flags == winfsp.GetAttributesByName {
		return 0, nil, err
	}
	attributes := target.FileAttributes
	var sd *windows.SECURITY_DESCRIPTOR
	if (flags & winfsp.GetSecurityByName) != 0 {
		// XXX: this is a mock up, the file is considered to
		// be owned by current process, so it is okay to
		// return the security descriptor of the process.
		sd, err = procsd.Load()
	}
	return attributes, sd, err
}

var _ winfsp.BehaviourGetSecurityByName = (*fileSystem)(nil)

const (
	// unsupportedCreateOptions are the options that are not
	// supported by the file system driver.
	//
	// There're many of them, but it is good to eliminate
	// behaviours that might violates the intention of the
	// caller processes and maintain the integrity of the
	// inner file system.
	unsupportedCreateOptions = windows.FILE_WRITE_THROUGH |
		windows.FILE_CREATE_TREE_CONNECTION |
		windows.FILE_NO_EA_KNOWLEDGE |
		windows.FILE_OPEN_BY_FILE_ID |
		windows.FILE_RESERVE_OPFILTER |
		windows.FILE_OPEN_REQUIRING_OPLOCK |
		windows.FILE_COMPLETE_IF_OPLOCKED

	// bothDirectoryFlags are the flags of directory or-ing
	// the non directory flags. If both flags are set, this
	// is obsolutely an invalid flag, you know.
	bothDirectoryFlags = windows.FILE_DIRECTORY_FILE |
		windows.FILE_NON_DIRECTORY_FILE
)

func (fs *fileSystem) openFile(
	ref *winfsp.FileSystemRef, name string,
	createOptions, grantedAccess uint32, mode os.FileMode,
	info *winfsp.FSP_FSCTL_FILE_INFO,
) (uintptr, error) {
	if createOptions&unsupportedCreateOptions != 0 {
		return 0, windows.STATUS_INVALID_PARAMETER
	}
	if createOptions&bothDirectoryFlags == bothDirectoryFlags {
		return 0, windows.STATUS_INVALID_PARAMETER
	}
	var err error

	// Determine the current access flag for writer here.
	flags := 0
	accessFlags := 0
	readAccess := grantedAccess & windows.FILE_READ_DATA
	writeAccess := grantedAccess &
		(windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA)
	switch {
	case readAccess == 0 && writeAccess == 0:
	case readAccess != 0 && writeAccess == 0:
		accessFlags = os.O_RDONLY
	case readAccess == 0 && writeAccess != 0:
		accessFlags = os.O_WRONLY
	case readAccess != 0 && writeAccess != 0:
		accessFlags = os.O_RDWR
	}
	if writeAccess == windows.FILE_APPEND_DATA {
		flags |= os.O_APPEND
	}

	// Determine the creation mode for writer here.
	//
	// TODO: I've not studied the dispositions here carefully
	// so the actual behaviour might be bizarre, and it would
	// be helpful of you to correct them.
	disposition := (createOptions >> 24) & 0x0ff
	switch disposition {
	case windows.FILE_SUPERSEDE:
		flags |= os.O_CREATE | os.O_TRUNC
	case windows.FILE_CREATE:
		flags |= os.O_CREATE | os.O_EXCL
	case windows.FILE_OPEN:
	case windows.FILE_OPEN_IF:
		flags |= os.O_CREATE
	case windows.FILE_OVERWRITE:
		flags |= os.O_TRUNC
	case windows.FILE_OVERWRITE_IF:
		flags |= os.O_CREATE | os.O_TRUNC
	default:
		return 0, windows.STATUS_INVALID_PARAMETER
	}

	// Lock the file with desired mode.

	// We are allowed to wait for the write operation
	// for a more fluent user experience.
	// TODO: create an option to control it?
	lockFunc := fs.locker.RLockFile
	if (createOptions&windows.FILE_DELETE_ON_CLOSE != 0) ||
		(grantedAccess&windows.DELETE != 0) ||
		(disposition == windows.FILE_SUPERSEDE) {
		lockFunc = fs.locker.TryWLockFile
	}
	lock := lockFunc(name)
	if lock == nil {
		return 0, windows.STATUS_SHARING_VIOLATION
	}
	defer func() { lock.Unlock() }()
	created := false
	node := lock.RetainNode()
	defer func() {
		if !created {
			node.Free()
		}
	}()

	// XXX: currently there's no direct interface
	// to express the semantic of FILE_SUPERSEDE.
	// Since the file has been opened with write
	// lock, and no more new open file can be
	// created before us returning, thus it
	// suffices to check the number of references.
	if flags&windows.FILE_SUPERSEDE != 0 {
		if lock.CurrentRefs() > 1 {
			return 0, windows.STATUS_ACCESS_DENIED
		}
	}

	// Attempt to allocate the file handle.
	handle := &fileHandle{
		node: node,
	}
	handleAddr := uintptr(unsafe.Pointer(handle))
	_, loaded := fs.handles.LoadOrStore(handleAddr, handle)
	if loaded {
		return 0, windows.ERROR_NOT_ENOUGH_MEMORY
	}
	defer func() {
		if !created {
			fs.handles.Delete(handleAddr)
		}
	}()

	// Normalize the path to ensure identity of operation.
	name = lock.FilePath()

	// See if we are asked to create directories here.
	if (createOptions&windows.FILE_DIRECTORY_FILE != 0) &&
		(flags&os.O_CREATE != 0) {
		if flags&os.O_TRUNC != 0 {
			return 0, windows.STATUS_INVALID_PARAMETER
		}
		mode |= os.FileMode(0111)
		if err := fs.inner.Mkdir(name, mode); err != nil {
			if os.IsExist(err) ||
				errors.Is(err, windows.STATUS_OBJECT_NAME_COLLISION) {
				err = windows.STATUS_OBJECT_NAME_COLLISION
				if flags&os.O_EXCL == 0 {
					err = nil
				}
			}
			if err != nil {
				return 0, err
			}
		}

		// Clear the flags since the create directory has
		// already been handled properly above.
		flags = 0
		mode = os.FileMode(0)
		accessFlags = os.O_RDONLY
	}

	// Attempt to open the file in the underlying file system.
	dirCheckErr := windows.STATUS_NOT_A_DIRECTORY
	file, err := fs.inner.OpenFile(name, accessFlags|flags, mode)
	if err != nil {
		// We will only try again if it complains about opening a
		// directory file failed, but we should be able to open the
		// directory with POSIX compatible flags.
		//
		// We will perform extra check to ensure we have really
		// opened a directory rather than been entangled in some
		// TOCTOU scenario.
		//
		// XXX: The O_RDONLY, O_WRONLY and O_APPEND flags (or their
		// preimages FILE_LIST_DIRECTORY, FILE_ADD_FILE and
		// FILE_ADD_SUBDIRECTORY) are not mandatory. All these
		// operations are retranslated into POSIX style operations.
		if (createOptions&bothDirectoryFlags !=
			windows.FILE_NON_DIRECTORY_FILE) &&
			(errors.Is(err, syscall.EISDIR) ||
				errors.Is(err, windows.STATUS_FILE_IS_A_DIRECTORY) ||
				errors.Is(err, windows.ERROR_DIRECTORY)) {
			accessFlags = os.O_RDONLY
			flags = 0
			file, err = fs.inner.OpenFile(name, accessFlags|flags, mode)
			createOptions |= windows.FILE_DIRECTORY_FILE
			dirCheckErr = windows.STATUS_OBJECT_NAME_NOT_FOUND
		}
		if err != nil {
			return 0, err
		}
	}
	defer func() {
		if !created {
			_ = file.Close()
		}
	}()
	handle.file = file
	handle.flags = accessFlags | (flags & os.O_APPEND)

	// Judge whether this is the stuff we would like to open.
	fileInfo, err := file.Stat()
	if err != nil {
		return 0, err
	}
	switch createOptions & bothDirectoryFlags {
	case windows.FILE_DIRECTORY_FILE:
		if !fileInfo.IsDir() {
			return 0, dirCheckErr
		}
	case windows.FILE_NON_DIRECTORY_FILE:
		if fileInfo.IsDir() {
			return 0, windows.STATUS_FILE_IS_A_DIRECTORY
		}
	default:
	}

	// Evaluate the file index for the file and cache it.
	handle.evaluatedIndex = lock.AddrAsID()

	// Copy the status out to the file information block.
	//
	// XXX: This must always be done after all fields in
	// the handle are filled.
	err = fs.fillInfoFromHandleLocked(info, handle, fileInfo, nil)
	if err != nil {
		return 0, err
	}

	// Finish opening the file and return to the caller.
	created = true
	return handleAddr, nil
}

func (fs *fileSystem) Create(
	ref *winfsp.FileSystemRef, name string,
	createOptions, grantedAccess, fileAttributes uint32,
	securityDescriptor *windows.SECURITY_DESCRIPTOR,
	allocationSize uint64, info *winfsp.FSP_FSCTL_FILE_INFO,
) (uintptr, error) {
	fileMode := os.FileMode(0444)
	if fileAttributes&windows.FILE_ATTRIBUTE_READONLY == 0 {
		fileMode |= os.FileMode(0666)
	}
	if fileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		fileMode |= os.FileMode(0111)
	}
	return fs.openFile(
		ref, name, createOptions, grantedAccess,
		fileMode, info,
	)
}

var _ winfsp.BehaviourCreate = (*fileSystem)(nil)

func (fs *fileSystem) Open(
	ref *winfsp.FileSystemRef, name string,
	createOptions, grantedAccess uint32,
	info *winfsp.FSP_FSCTL_FILE_INFO,
) (uintptr, error) {
	return fs.openFile(
		ref, name, createOptions, grantedAccess,
		os.FileMode(0), info,
	)
}

func (fs *fileSystem) load(file uintptr) (*fileHandle, error) {
	obj, ok := fs.handles.Load(file)
	if !ok {
		return nil, windows.STATUS_INVALID_HANDLE
	}
	return obj.(*fileHandle), nil
}

func (fs *fileSystem) Close(
	ref *winfsp.FileSystemRef, file uintptr,
) {
	object, ok := fs.handles.LoadAndDelete(file)
	if !ok {
		return
	}
	fileHandle := object.(*fileHandle)
	fileHandle.mtx.Lock()
	defer fileHandle.mtx.Unlock()
	defer fileHandle.node.Free()
	defer fileHandle.dir.Delete()
	if fileHandle.file != nil {
		_ = fileHandle.file.Close()
		fileHandle.file = nil
	}
}

func (handle *fileHandle) lockChecked() error {
	handle.mtx.RLock()
	valid := false
	defer func() {
		if !valid {
			handle.mtx.RUnlock()
		}
	}()
	if handle.file == nil {
		return windows.STATUS_INVALID_HANDLE
	}
	valid = true
	return nil
}

func (handle *fileHandle) unlockChecked() {
	handle.mtx.RUnlock()
}

var _ winfsp.BehaviourBase = (*fileSystem)(nil)

func (fs *fileSystem) Overwrite(
	ref *winfsp.FileSystemRef, file uintptr,
	attributes uint32, replaceAttributes bool,
	allocationSize uint64,
	info *winfsp.FSP_FSCTL_FILE_INFO,
) error {
	var err error
	handle, err := fs.load(file)
	if err != nil {
		return err
	}
	if err := handle.lockChecked(); err != nil {
		return err
	}
	defer handle.unlockChecked()
	if err := handle.file.Truncate(0); err != nil {
		return err
	}
	// TODO: support chmod operation in the future.
	//
	// It might seems like we are just ignoring the attribute
	// update but we might support them in the future.
	err = fs.fillInfoFromHandle(info, handle, nil, nil)
	if err != nil {
		return err
	}
	return nil
}

var _ winfsp.BehaviourOverwrite = (*fileSystem)(nil)

func (fs *fileSystem) GetOrNewDirBuffer(
	ref *winfsp.FileSystemRef, file uintptr,
) (*winfsp.DirBuffer, error) {
	fileHandle, err := fs.load(file)
	if err != nil {
		return nil, err
	}
	return &fileHandle.dir, nil
}

func (fs *fileSystem) ReadDirectory(
	ref *winfsp.FileSystemRef, file uintptr, pattern string,
	fill func(string, *winfsp.FSP_FSCTL_FILE_INFO) (bool, error),
) error {
	handle, err := fs.load(file)
	if err != nil {
		return err
	}
	if err := handle.lockChecked(); err != nil {
		return err
	}
	defer handle.unlockChecked()
	plock := handle.node.RLockPath()
	defer plock.Unlock()
	// If the directory has been deleted, then
	// we will fail the read operation.
	if plock.IsExile() {
		return os.ErrNotExist
	}
	f, err := fs.inner.OpenFile(
		plock.FilePath(), handle.flags, os.FileMode(0))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	parentInfo, err := f.Stat()
	if err != nil {
		return err
	}
	fileInfos, err := f.Readdir(-1)
	if err != nil {
		return err
	}
	for _, fileInfo := range fileInfos {
		var info winfsp.FSP_FSCTL_FILE_INFO
		fs.fillInfoFromSelfParentStats(&info, fileInfo, parentInfo, 0)
		ok, err := fill(fileInfo.Name(), &info)
		if err != nil || !ok {
			return err
		}
	}
	return nil
}

var _ winfsp.BehaviourReadDirectory = (*fileSystem)(nil)

func (fs *fileSystem) GetFileInfo(
	ref *winfsp.FileSystemRef, file uintptr,
	info *winfsp.FSP_FSCTL_FILE_INFO,
) error {
	handle, err := fs.load(file)
	if err != nil {
		return err
	}
	if err := handle.lockChecked(); err != nil {
		return err
	}
	defer handle.unlockChecked()
	return fs.fillInfoFromHandle(info, handle, nil, nil)
}

var _ winfsp.BehaviourGetFileInfo = (*fileSystem)(nil)

func (fs *fileSystem) GetSecurity(
	ref *winfsp.FileSystemRef, file uintptr,
) (*windows.SECURITY_DESCRIPTOR, error) {
	_, err := fs.load(file)
	if err != nil {
		return nil, err
	}
	return procsd.Load()
}

var _ winfsp.BehaviourGetSecurity = (*fileSystem)(nil)

func (fs *fileSystem) GetVolumeInfo(
	ref *winfsp.FileSystemRef, info *winfsp.FSP_FSCTL_VOLUME_INFO,
) error {
	// TODO: support file system remaining size query.
	info.TotalSize = 8 * 1024 * 1024 * 1024 * 1024 // 8TB
	info.FreeSize = info.TotalSize
	length := fs.labelLen
	info.VolumeLabelLength = 2 * uint16(copy(
		info.VolumeLabel[:length], fs.label[:length]))
	return nil
}

var _ winfsp.BehaviourGetVolumeInfo = (*fileSystem)(nil)

func (fs *fileSystem) SetVolumeLabel(
	ref *winfsp.FileSystemRef, label string,
	info *winfsp.FSP_FSCTL_VOLUME_INFO,
) error {
	utf16, err := windows.UTF16FromString(label)
	if err != nil {
		return err
	}
	fs.labelLen = copy(fs.label[:], utf16)
	return fs.GetVolumeInfo(ref, info)
}

var _ winfsp.BehaviourSetVolumeLabel = (*fileSystem)(nil)

func (fs *fileSystem) SetBasicInfo(
	ref *winfsp.FileSystemRef, file uintptr,
	flags winfsp.SetBasicInfoFlags, attribute uint32,
	creationTime, lastAccessTime, lastWriteTime, changeTime uint64,
	info *winfsp.FSP_FSCTL_FILE_INFO,
) error {
	var err error
	handle, err := fs.load(file)
	if err != nil {
		return err
	}
	if err := handle.lockChecked(); err != nil {
		return err
	}
	defer handle.unlockChecked()
	err = fs.fillInfoFromHandle(info, handle, nil, nil)
	if err != nil {
		return err
	}
	return windows.STATUS_ACCESS_DENIED
}

var _ winfsp.BehaviourSetBasicInfo = (*fileSystem)(nil)

// FileTruncateEx is the truncate interface related to Windows
// style opertations. Without this interface, we will be
// imitating the set allocation size behaviour of file, making
// it behaves stragely under certain racing circumstances.
type FileTruncateEx interface {
	File

	// Shrink means it will not expand the file size if a size
	// greater than the file size is passed.
	Shrink(newSize int64) error
}

type fileMimicTruncate struct {
	File
}

func (f *fileMimicTruncate) Shrink(newSize int64) error {
	fileInfo, err := f.Stat()
	if err != nil {
		return err
	}
	if fileInfo.Size() > newSize {
		return f.Truncate(newSize)
	}
	return nil
}

func (fs *fileSystem) SetFileSize(
	ref *winfsp.FileSystemRef, file uintptr,
	newSize uint64, setAllocationSize bool,
	info *winfsp.FSP_FSCTL_FILE_INFO,
) error {
	handle, err := fs.load(file)
	if err != nil {
		return err
	}
	if err := handle.lockChecked(); err != nil {
		return err
	}
	defer handle.unlockChecked()
	size := int64(newSize)
	if setAllocationSize {
		var shrinker FileTruncateEx
		if obj, ok := handle.file.(FileTruncateEx); ok {
			shrinker = obj
		} else {
			shrinker = &fileMimicTruncate{
				File: handle.file,
			}
		}
		if err := shrinker.Shrink(size); err != nil {
			return err
		}
	} else {
		if err := handle.file.Truncate(size); err != nil {
			return err
		}
	}
	return fs.fillInfoFromHandle(info, handle, nil, nil)
}

var _ winfsp.BehaviourSetFileSize = (*fileSystem)(nil)

func (fs *fileSystem) Read(
	ref *winfsp.FileSystemRef, file uintptr,
	buf []byte, offset uint64,
) (int, error) {
	handle, err := fs.load(file)
	if err != nil {
		return 0, err
	}
	if err := handle.lockChecked(); err != nil {
		return 0, err
	}
	defer handle.unlockChecked()
	// No matter random access or append only file handle
	// on windows should support random read.
	return handle.file.ReadAt(buf, int64(offset))
}

var _ winfsp.BehaviourRead = (*fileSystem)(nil)

// FileWriteEx is the write interface related to Windows style
// writing. Without this interface, we will be imitating the
// write behaviour of file, making it behaves strangely under
// certain racing circumstances.
type FileWriteEx interface {
	File

	// Append means the data will always be written to the
	// tail of the file, regardless of the file's current
	// open mode.
	Append([]byte) (int, error)

	// ConstrainedWriteAt means the data will be written at
	// specified offset and the data within the file's size
	// range will be copied out.
	ConstrainedWriteAt([]byte, int64) (int, error)
}

type fileMimicWrite struct {
	File
	flags int
}

func (f *fileMimicWrite) Append(b []byte) (int, error) {
	if f.flags&os.O_APPEND != 0 {
		return f.Write(b)
	} else {
		// BUG: since we imitates the append behaviour
		// by fetching the file size first and then
		// appending to it, two concurrent append
		// operations will overlaps with each other.
		fileInfo, err := f.Stat()
		if err != nil {
			return 0, err
		}
		return f.WriteAt(b, fileInfo.Size())
	}
}

func (f *fileMimicWrite) ConstrainedWriteAt(
	b []byte, offset int64,
) (int, error) {
	// BUG: this is also a buggy part when two
	// concurrent write operation happens. You
	// might expect the reordering of constrained
	// write operation and an boundary extending
	// operation.
	fileInfo, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := fileInfo.Size()
	if offset >= size {
		return 0, nil
	} else if offset+int64(len(b)) >= size {
		b = b[:len(b)+int(size-offset)]
	}
	return f.WriteAt(b, offset)
}

func (fs *fileSystem) Write(
	ref *winfsp.FileSystemRef, file uintptr,
	b []byte, offset uint64,
	writeToEndOfFile, constrainedIo bool,
	info *winfsp.FSP_FSCTL_FILE_INFO,
) (int, error) {
	handle, err := fs.load(file)
	if err != nil {
		return 0, err
	}
	if (handle.flags&os.O_APPEND != 0) && !writeToEndOfFile {
		// You may not write to an append-only file.
		return 0, windows.STATUS_ACCESS_DENIED
	}
	if err := handle.lockChecked(); err != nil {
		return 0, err
	}
	defer handle.unlockChecked()
	var writer FileWriteEx
	if obj, ok := handle.file.(FileWriteEx); ok {
		writer = obj
	} else {
		writer = &fileMimicWrite{
			File:  handle.file,
			flags: handle.flags,
		}
	}
	var n int
	if writeToEndOfFile && constrainedIo {
		// Nothing to do here.
	} else if writeToEndOfFile {
		n, err = writer.Append(b)
	} else if constrainedIo {
		n, err = writer.ConstrainedWriteAt(b, int64(offset))
	} else {
		n, err = handle.file.WriteAt(b, int64(offset))
	}
	if info != nil {
		// XXX: Since the driver code just take the information
		// field for notification and display purpose, so only
		// the lastly updated information is required.
		//
		// TODO: What pieces of information is required by the
		// driver? Can we optimize the number of `Stat`s if
		// the FileAttributes is actually not needed?
		statErr := fs.fillInfoFromHandle(info, handle, nil, nil)
		if statErr != nil && err == nil {
			err = statErr
		}
	}
	return n, err
}

var _ winfsp.BehaviourWrite = (*fileSystem)(nil)

func (fs *fileSystem) Flush(
	ref *winfsp.FileSystemRef, file uintptr,
	info *winfsp.FSP_FSCTL_FILE_INFO,
) error {
	if file == 0 {
		// Flush the whole filesystem, not a single file.
		return nil
	}
	handle, err := fs.load(file)
	if err != nil {
		return err
	}
	if err := handle.lockChecked(); err != nil {
		return err
	}
	defer handle.unlockChecked()
	if err := handle.file.Sync(); err != nil {
		return err
	}
	// TODO: Again, is it the same case as `Stat`-ing
	// in the Write method?
	return fs.fillInfoFromHandle(info, handle, nil, nil)
}

var _ winfsp.BehaviourFlush = (*fileSystem)(nil)

func (fs *fileSystem) CanDelete(
	ref *winfsp.FileSystemRef, file uintptr,
	name string,
) error {
	handle, err := fs.load(file)
	if err != nil {
		return err
	}
	if err := handle.lockChecked(); err != nil {
		return err
	}
	defer handle.unlockChecked()

	plock := handle.node.TryWLockPath()
	if plock == nil {
		return windows.STATUS_ACCESS_DENIED
	}
	defer plock.Unlock()
	// The file has been deleted.
	if plock.IsExile() {
		return windows.STATUS_OBJECT_NAME_NOT_FOUND
	}

	// There's possibly node opening files under
	// this node, which must fail the operation.
	if plock.HasChild() {
		return windows.STATUS_ACCESS_DENIED
	}

	fileInfo, err := handle.file.Stat()
	if err != nil {
		return err
	}
	if !fileInfo.IsDir() {
		return nil
	}
	f, err := fs.inner.OpenFile(
		plock.FilePath(), handle.flags, os.FileMode(0))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	fileInfos, err := f.Readdir(-1)
	if err != nil {
		return err
	}
	if len(fileInfos) > 0 {
		return windows.STATUS_DIRECTORY_NOT_EMPTY
	}
	return nil
}

var _ winfsp.BehaviourCanDelete = (*fileSystem)(nil)

func (fs *fileSystem) Cleanup(
	ref *winfsp.FileSystemRef, file uintptr,
	name string, cleanupFlags uint32,
) {
	handle, err := fs.load(file)
	if err != nil {
		return
	}
	if cleanupFlags&winfsp.FspCleanupDelete == 0 {
		return
	}
	handle.mtx.Lock()
	defer handle.mtx.Unlock()
	if handle.file == nil {
		return
	}
	plock := handle.node.TryWLockPath()
	if plock == nil {
		return
	}
	defer plock.Unlock()
	if plock.IsExile() {
		return
	}
	exile := fs.locker.AllocExile()
	defer exile.Free()
	exileLock := exile.TryWLockPath()
	// assert exileLock != nil
	if exileLock == nil {
		panic("write lock exile node failed")
	}
	defer exileLock.Unlock()
	_ = handle.file.Close()
	handle.file = nil
	if err := fs.inner.Remove(plock.FilePath()); err != nil {
		return
	}
	treelock.Exchange(plock, exileLock)
}

var _ winfsp.BehaviourCleanup = (*fileSystem)(nil)

func (fs *fileSystem) Rename(
	ref *winfsp.FileSystemRef, file uintptr,
	source, target string, replaceIfExist bool,
) error {
	handle, err := fs.load(file)
	if err != nil {
		return err
	}
	handle.mtx.Lock()
	defer handle.mtx.Unlock()
	if handle.file == nil {
		return windows.STATUS_INVALID_HANDLE
	}
	oldLock := handle.node.TryWLockPath()
	if oldLock == nil {
		return windows.STATUS_SHARING_VIOLATION
	}
	defer oldLock.Unlock()
	if oldLock.IsExile() {
		return windows.STATUS_OBJECT_NAME_NOT_FOUND
	}

	// Try to grab the target path's lock.
	newLock := fs.locker.TryWLockFile(target)
	if newLock == nil {
		return windows.STATUS_SHARING_VIOLATION
	}
	defer newLock.Unlock()

	// Normalize the target name.
	target = newLock.FilePath()

	// Check for the rename precondition so that we could
	// avoid performing sophiscated operations.
	if !replaceIfExist {
		fileInfo, err := fs.inner.Stat(target)
		if err != nil && !os.IsNotExist(err) &&
			!errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND) {
			return err
		}
		if fileInfo != nil {
			return windows.STATUS_OBJECT_NAME_COLLISION
		}
	}

	// Close the file temporarily, since in some filesystem,
	// opening the file will cause the move operation to fail.
	//
	// Upon exit, the remaining file will be reopened and
	// seek to its orignal offset, so that we can continue
	// our operations.
	fileInfo, err := handle.file.Stat()
	if err != nil {
		return err
	}
	var pos *int64
	if fileInfo.Mode().IsRegular() {
		value, err := handle.file.Seek(0, os.SEEK_CUR)
		if err != nil {
			return err
		}
		pos = new(int64)
		*pos = value
	}
	_ = handle.file.Close()
	handle.file = nil
	defer func() {
		// It's either the file moved successfully so that the
		// handle.node get placed under the target directory,
		// or the file failing to move so that handle.node
		// stays in its original position. Either case we
		// can trust the file path from handle.node.FilePath().
		f, err := fs.inner.OpenFile(
			handle.node.FilePath(), handle.flags, os.FileMode(0))
		if err != nil {
			return
		}
		defer func() {
			if f != nil {
				_ = f.Close()
			}
		}()
		if pos != nil {
			if _, err := f.Seek(*pos, os.SEEK_SET); err != nil {
				return
			}
		}
		handle.file, f = f, nil
	}()

	// Attempt to perform the rename operation now.
	source = oldLock.FilePath()
	if err := fs.inner.Rename(source, target); err != nil {
		return err
	}
	treelock.Exchange(oldLock, newLock)
	return nil
}

var _ winfsp.BehaviourRename = (*fileSystem)(nil)

type newOption struct {
	attribReadOnlyTransMode AttribReadOnlyTransMode
}

// NewOption is the optional option used to
// initialize the gofs.
type NewOption func(*newOption) error

func WithAttribReadOnlyTransMode(mode AttribReadOnlyTransMode) NewOption {
	return func(option *newOption) (rerr error) {
		defer func() {
			if rerr == nil {
				return
			}
			rerr = errors.Wrapf(
				rerr, "apply WithAttribReadOnlyTransMode(%d)", uint64(mode),
			)
		}()
		if (mode & AttribReadOnlyAllBits) != mode {
			return errors.New("invalid attribute bits")
		}
		switch mode & AttribReadOnlyAllStyleBits {
		case AttribReadOnlyWindows:
		case AttribReadOnlyBypass:
		case AttribReadOnlyAlways:
		case AttribReadOnlyPOSIX:
		default:
			return errors.New("invalid style")
		}
		option.attribReadOnlyTransMode = mode
		return nil
	}
}

// NewOptions create the file system with
// the provided `gofs.FileSystem` and a
// variadic array of options.
func NewOptions(
	fs FileSystem, opts ...NewOption,
) (winfsp.BehaviourBase, error) {
	var option newOption
	for _, opt := range opts {
		if err := opt(&option); err != nil {
			return nil, err
		}
	}
	return &fileSystem{
		inner:             fs,
		locker:            treelock.New(),
		readOnlyTransMode: option.attribReadOnlyTransMode,
	}, nil
}

// New create the file system with the
// provided `gofs.FileSystem` and default
// settings, which is guaranteed to success.
func New(fs FileSystem) winfsp.BehaviourBase {
	result, err := NewOptions(fs)
	if err != nil {
		panic(err)
	}
	return result
}
