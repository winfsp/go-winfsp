package winfsp

import (
	"io"
	"math"
	"os"
	"reflect"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/pkg/errors"
	"golang.org/x/sys/windows"
)

// FileSystemRef is the reference for the file system,
// with which the callers can operate and manipulate the
// file system, except for destroying it.
type FileSystemRef struct {
	fileSystemOps         *FSP_FILE_SYSTEM_INTERFACE
	fileSystem            *FSP_FILE_SYSTEM
	base                  BehaviourBase
	getVolumeInfo         BehaviourGetVolumeInfo
	setVolumeLabel        BehaviourSetVolumeLabel
	getSecurityByName     BehaviourGetSecurityByName
	create                BehaviourCreate
	overwrite             BehaviourOverwrite
	cleanup               BehaviourCleanup
	read                  BehaviourRead
	write                 BehaviourWrite
	flush                 BehaviourFlush
	getFileInfo           BehaviourGetFileInfo
	setBasicInfo          BehaviourSetBasicInfo
	setFileSize           BehaviourSetFileSize
	canDelete             BehaviourCanDelete
	rename                BehaviourRename
	getSecurity           BehaviourGetSecurity
	setSecurity           BehaviourSetSecurity
	readDirRaw            BehaviourReadDirectoryRaw
	getDirInfoByName      BehaviourGetDirInfoByName
	deviceIoControl       BehaviourDeviceIoControl
	createEx              BehaviourCreateEx
	deleteReparsePoint    BehaviourDeleteReparsePoint
	getReparsePoint       BehaviourGetReparsePoint
	getReparsePointByName BehaviourGetReparsePointByName
	setReparsePoint       BehaviourSetReparsePoint
}

// ntStatusNoRef is returned when user context to inner
// map is not present.
const ntStatusNoRef = windows.STATUS_DEVICE_OFF_LINE

var refMap sync.Map

func loadFileSystemRef(fileSystem uintptr) *FileSystemRef {
	fsp := (*FSP_FILE_SYSTEM)(unsafe.Pointer(fileSystem))
	value, ok := refMap.Load(fsp.UserContext)
	if !ok {
		return nil
	}
	return value.(*FileSystemRef)
}

var syscallNTStatusMap = map[syscall.Errno]windows.NTStatus{
	syscall.Errno(0): windows.STATUS_SUCCESS,

	// Application errors conversion map.
	syscall.ENOENT:  windows.STATUS_OBJECT_NAME_NOT_FOUND,
	syscall.EEXIST:  windows.STATUS_OBJECT_NAME_COLLISION,
	syscall.EPERM:   windows.STATUS_ACCESS_DENIED,
	syscall.ENOTDIR: windows.STATUS_NOT_A_DIRECTORY,
	syscall.EISDIR:  windows.STATUS_FILE_IS_A_DIRECTORY,
	syscall.EINVAL:  windows.STATUS_INVALID_PARAMETER,

	// System errors conversion map.
	syscall.ERROR_ACCESS_DENIED: windows.STATUS_ACCESS_DENIED,
	//syscall.ERROR_FILE_NOT_FOUND:  windows.STATUS_OBJECT_NAME_NOT_FOUND,
	//syscall.ERROR_PATH_NOT_FOUND:  windows.STATUS_OBJECT_NAME_NOT_FOUND,
	syscall.ERROR_NOT_FOUND:       windows.STATUS_OBJECT_NAME_NOT_FOUND,
	syscall.ERROR_FILE_EXISTS:     windows.STATUS_OBJECT_NAME_COLLISION,
	syscall.ERROR_ALREADY_EXISTS:  windows.STATUS_OBJECT_NAME_COLLISION,
	syscall.ERROR_BUFFER_OVERFLOW: windows.STATUS_BUFFER_OVERFLOW,
	syscall.ERROR_DIR_NOT_EMPTY:   windows.STATUS_DIRECTORY_NOT_EMPTY,
}

func convertNTStatus(err error) windows.NTStatus {
	if err == nil {
		return windows.STATUS_SUCCESS
	}
	var status windows.NTStatus
	if errors.As(err, &status) {
		return status
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if status, ok := syscallNTStatusMap[errno]; ok {
			return status
		}
	}
	if errors.Is(err, io.EOF) {
		return windows.STATUS_END_OF_FILE
	}
	if errors.Is(err, os.ErrExist) {
		return windows.STATUS_OBJECT_NAME_COLLISION
	}
	if errors.Is(err, os.ErrNotExist) {
		return windows.STATUS_OBJECT_NAME_NOT_FOUND
	}
	if errors.Is(err, os.ErrPermission) {
		return windows.STATUS_ACCESS_DENIED
	}
	return windows.STATUS_INTERNAL_ERROR
}

func utf16PtrToString(ptr uintptr) string {
	utf16Ptr := (*uint16)(unsafe.Pointer(ptr))
	return windows.UTF16PtrToString(utf16Ptr)
}

func enforceBytePtr(ptr uintptr, size int) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(ptr)), size)
}

// FileSystem is the created object of WinFSP's filesystem.
//
// Most behaviour of the file system are defined for the
// FileSystemRef object, except for the resource management
// ones. The FileSystem object will be recycled automatically
// when there's no reference to it.
type FileSystem struct {
	FileSystemRef
}

// BehaviourBase defines the mandatory methods.
//
// Other methods might be implemented and will be checked
// upon mounting the filesystem.
type BehaviourBase interface {
	// Open the file specified by name.
	Open(
		fs *FileSystemRef, name string,
		createOptions, grantedAccess uint32,
		info *FSP_FSCTL_FILE_INFO,
	) (uintptr, error)

	// Close a open file handle.
	Close(fs *FileSystemRef, file uintptr)
}

func delegateOpen(
	fileSystem, fileName uintptr,
	createOptions, grantedAccess uint32,
	file *uintptr, fileInfoAddr uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	result, err := ref.base.Open(
		ref, utf16PtrToString(fileName),
		createOptions, grantedAccess,
		(*FSP_FSCTL_FILE_INFO)(
			unsafe.Pointer(fileInfoAddr)),
	)
	if err != nil {
		return convertNTStatus(err)
	}
	*file = result
	return windows.STATUS_SUCCESS
}

var go_delegateOpen = syscall.NewCallbackCDecl(func(
	fileSystem, fileName uintptr,
	createOptions, grantedAccess uint32,
	file *uintptr, fileInfoAddr uintptr,
) uintptr {
	return uintptr(delegateOpen(
		fileSystem, fileName,
		createOptions, grantedAccess,
		file, fileInfoAddr,
	))
})

func delegateClose(fileSystem, file uintptr) {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return
	}
	ref.base.Close(ref, file)
}

var go_delegateClose = syscall.NewCallbackCDecl(func(
	fileSystem, file uintptr,
) uintptr {
	delegateClose(fileSystem, file)
	return uintptr(windows.STATUS_SUCCESS)
})

// BehaviourGetVolumeInfo retrieves volume info.
type BehaviourGetVolumeInfo interface {
	GetVolumeInfo(
		fs *FileSystemRef, info *FSP_FSCTL_VOLUME_INFO,
	) error
}

func delegateGetVolumeInfo(
	fileSystem, volumeInfoAddr uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	return convertNTStatus(ref.getVolumeInfo.GetVolumeInfo(
		ref, (*FSP_FSCTL_VOLUME_INFO)(
			unsafe.Pointer(volumeInfoAddr)),
	))
}

var go_delegateGetVolumeInfo = syscall.NewCallbackCDecl(func(
	fileSystem, volumeInfoAddr uintptr,
) uintptr {
	return uintptr(delegateGetVolumeInfo(
		fileSystem, volumeInfoAddr,
	))
})

// BehaviourSetVolumeLabel sets volume label.
type BehaviourSetVolumeLabel interface {
	SetVolumeLabel(
		fs *FileSystemRef, label string,
		info *FSP_FSCTL_VOLUME_INFO,
	) error
}

func delegateSetVolumeLabel(
	fileSystem, labelAddr, volumeInfoAddr uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	return convertNTStatus(ref.setVolumeLabel.SetVolumeLabel(
		ref, utf16PtrToString(labelAddr),
		(*FSP_FSCTL_VOLUME_INFO)(
			unsafe.Pointer(volumeInfoAddr)),
	))
}

var go_delegateSetVolumeLabel = syscall.NewCallbackCDecl(func(
	fileSystem, labelAddr, volumeInfoAddr uintptr,
) uintptr {
	return uintptr(delegateSetVolumeLabel(
		fileSystem, labelAddr, volumeInfoAddr,
	))
})

// GetSecurityByNameFlags indicates the content that the
// caller cares about. The callee can return null value on
// the item that is not interested in.
type GetSecurityByNameFlags uint8

const (
	GetExistenceOnly = GetSecurityByNameFlags(iota)
	GetAttributesByName
	GetSecurityByName
	GetAttributesSecurity
)

// BehaviourGetSecurityByName retrieves file attributes and
// security descriptor by file name.
//
// The file attribute can also be a reparse point index when
// windows.STATUS_REPARSE is returned.
type BehaviourGetSecurityByName interface {
	GetSecurityByName(
		fs *FileSystemRef, name string,
		flags GetSecurityByNameFlags,
	) (uint32, *windows.SECURITY_DESCRIPTOR, error)
}

func delegateGetSecurityByName(
	fileSystem, fileName, attributesAddr uintptr,
	securityDescAddr, securityDescSizeAddr uintptr,
) windows.NTStatus {
	flags := GetExistenceOnly
	attributes := (*uint32)(unsafe.Pointer(attributesAddr))
	if attributes != nil {
		flags |= GetAttributesByName
		*attributes = 0
	}
	size := (*uintptr)(unsafe.Pointer(securityDescSizeAddr))
	var bufferSize int
	if size != nil {
		flags |= GetSecurityByName
		bufferSize = int(*size)
		*size = 0
	}
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	attr, sd, err := ref.getSecurityByName.GetSecurityByName(
		ref, utf16PtrToString(fileName), flags)
	if err != nil {
		return convertNTStatus(err)
	}
	if attributes != nil {
		*attributes = attr
	}
	if size != nil {
		length := int(sd.Length())
		*size = uintptr(length)
		source := enforceBytePtr(uintptr(unsafe.Pointer(sd)), length)
		target := enforceBytePtr(securityDescAddr, bufferSize)
		if copy(target, source) < length {
			return windows.STATUS_BUFFER_OVERFLOW
		}
	}
	return windows.STATUS_SUCCESS
}

var go_delegateGetSecurityByName = syscall.NewCallbackCDecl(func(
	fileSystem, fileName, attributesAddr uintptr,
	securityDescAddr, securityDescSizeAddr uintptr,
) uintptr {
	return uintptr(delegateGetSecurityByName(
		fileSystem, fileName, attributesAddr,
		securityDescAddr, securityDescSizeAddr,
	))
})

// BehaviourCreate creates a new file or directory.
type BehaviourCreate interface {
	Create(
		fs *FileSystemRef, name string,
		createOptions, grantedAccess, fileAttributes uint32,
		securityDescriptor *windows.SECURITY_DESCRIPTOR,
		allocationSize uint64, info *FSP_FSCTL_FILE_INFO,
	) (uintptr, error)
}

func delegateCreate(
	fileSystem, fileName uintptr,
	createOptions, grantedAccess, fileAttributes uint32,
	securityDescriptor uintptr, allocationSize uint64,
	file *uintptr, fileInfoAddr uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	result, err := ref.create.Create(
		ref, utf16PtrToString(fileName),
		createOptions, grantedAccess, fileAttributes,
		(*windows.SECURITY_DESCRIPTOR)(
			unsafe.Pointer(securityDescriptor)),
		allocationSize, (*FSP_FSCTL_FILE_INFO)(
			unsafe.Pointer(fileInfoAddr)),
	)
	if err != nil {
		return convertNTStatus(err)
	}
	*file = result
	return windows.STATUS_SUCCESS
}

var go_delegateCreate = syscall.NewCallbackCDecl(func(
	fileSystem, fileName uintptr,
	createOptions, grantedAccess, fileAttributes uint32,
	securityDescriptor uintptr, allocationSize uint64,
	file *uintptr, fileInfoAddr uintptr,
) uintptr {
	return uintptr(delegateCreate(
		fileSystem, fileName,
		createOptions, grantedAccess, fileAttributes,
		securityDescriptor, allocationSize,
		file, fileInfoAddr,
	))
})

// BehaviourOverwrite overwrites a file's attribute.
type BehaviourOverwrite interface {
	Overwrite(
		fs *FileSystemRef, file uintptr,
		attributes uint32, replaceAttributes bool,
		allocationSize uint64,
		info *FSP_FSCTL_FILE_INFO,
	) error
}

func delegateOverwrite(
	fileSystem, file uintptr,
	attributes uint32, replaceAttributes uint8,
	allocationSize uint64, fileInfoAddr uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	return convertNTStatus(ref.overwrite.Overwrite(
		ref, file, attributes, replaceAttributes != 0,
		allocationSize, (*FSP_FSCTL_FILE_INFO)(
			unsafe.Pointer(fileInfoAddr)),
	))
}

var go_delegateOverwrite = syscall.NewCallbackCDecl(func(
	fileSystem, file uintptr,
	attributes uint32, replaceAttributes uint8,
	allocationSize uint64, fileInfoAddr uintptr,
) uintptr {
	return uintptr(delegateOverwrite(
		fileSystem, file,
		attributes, replaceAttributes,
		allocationSize, fileInfoAddr,
	))
})

// BehaviourCleanup performs the cleanup behaviour.
type BehaviourCleanup interface {
	Cleanup(
		fs *FileSystemRef, file uintptr, name string,
		cleanupFlags uint32,
	)
}

func delegateCleanup(
	fileSystem, fileContext, filename uintptr,
	cleanupFlags uint32,
) {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return
	}
	ref.cleanup.Cleanup(
		ref, fileContext, utf16PtrToString(filename),
		cleanupFlags,
	)
}

var go_delegateCleanup = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext, filename uintptr,
	cleanupFlags uint32,
) uintptr {
	delegateCleanup(
		fileSystem, fileContext, filename,
		cleanupFlags,
	)
	return uintptr(windows.STATUS_SUCCESS)
})

// BehaviourRead read an open file.
type BehaviourRead interface {
	Read(
		fs *FileSystemRef, file uintptr,
		buf []byte, offset uint64,
	) (int, error)
}

func delegateRead(
	fileSystem, fileContext, buffer uintptr,
	offset uint64, length uint32, bytesRead *uint32,
) windows.NTStatus {
	*bytesRead = 0
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	n, err := ref.read.Read(ref, fileContext,
		enforceBytePtr(buffer, int(length)), offset)
	*bytesRead = uint32(n)
	// XXX: this is required otherwise windows kernel render
	// it as nothing read from the file instead.
	if n > 0 && err == io.EOF {
		err = nil
	}
	return convertNTStatus(err)
}

var go_delegateRead = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext, buffer uintptr,
	offset uint64, length uint32, bytesRead *uint32,
) uintptr {
	return uintptr(delegateRead(
		fileSystem, fileContext, buffer,
		offset, length, bytesRead,
	))
})

// BehaviourWrite writes an open file.
type BehaviourWrite interface {
	Write(
		fs *FileSystemRef, file uintptr,
		buf []byte, offset uint64,
		writeToEndOfFile, constrainedIo bool,
		info *FSP_FSCTL_FILE_INFO,
	) (int, error)
}

func delegateWrite(
	fileSystem, fileContext, buffer uintptr,
	offset uint64, length uint32,
	writeToEndOfFile, constrainedIo uint8,
	bytesWritten *uint32, fileInfoAddr uintptr,
) windows.NTStatus {
	*bytesWritten = 0
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	n, err := ref.write.Write(ref, fileContext,
		enforceBytePtr(buffer, int(length)), offset,
		writeToEndOfFile != 0, constrainedIo != 0,
		(*FSP_FSCTL_FILE_INFO)(
			unsafe.Pointer(fileInfoAddr)),
	)
	*bytesWritten = uint32(n)
	return convertNTStatus(err)
}

var go_delegateWrite = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext, buffer uintptr,
	offset uint64, length uint32,
	writeToEndOfFile, constrainedIo uint8,
	bytesWritten *uint32, fileInfoAddr uintptr,
) uintptr {
	return uintptr(delegateWrite(
		fileSystem, fileContext, buffer,
		offset, length,
		writeToEndOfFile, constrainedIo,
		bytesWritten, fileInfoAddr,
	))
})

// BehaviourFlush flushes a file or volume.
//
// When file is not NULL, the specific file will be flushed,
// otherwise the whole volume will be flushed.
type BehaviourFlush interface {
	Flush(
		fs *FileSystemRef, file uintptr,
		info *FSP_FSCTL_FILE_INFO,
	) error
}

func delegateFlush(
	fileSystem, fileContext, infoAddr uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	return convertNTStatus(ref.flush.Flush(
		ref, fileContext, (*FSP_FSCTL_FILE_INFO)(
			unsafe.Pointer(infoAddr)),
	))
}

var go_delegateFlush = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext, infoAddr uintptr,
) uintptr {
	return uintptr(delegateFlush(
		fileSystem, fileContext, infoAddr,
	))
})

// BehaviourGetFileInfo retrieves stat of file or directory.
type BehaviourGetFileInfo interface {
	GetFileInfo(
		fs *FileSystemRef, file uintptr,
		info *FSP_FSCTL_FILE_INFO,
	) error
}

func delegateGetFileInfo(
	fileSystem, fileContext, infoAddr uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	return convertNTStatus(ref.getFileInfo.GetFileInfo(
		ref, fileContext, (*FSP_FSCTL_FILE_INFO)(
			unsafe.Pointer(infoAddr)),
	))
}

var go_delegateGetFileInfo = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext, infoAddr uintptr,
) uintptr {
	return uintptr(delegateGetFileInfo(
		fileSystem, fileContext, infoAddr,
	))
})

// SetBasicInfoFlags specifies a set of modified values
// in the SetBasicInfoFlags call.
type SetBasicInfoFlags uint32

const (
	SetBasicInfoAttributes = SetBasicInfoFlags(1 << iota)
	SetBasicInfoCreationTime
	SetBasicInfoLastAccessTime
	SetBasicInfoLastWriteTime
	SetBasicInfoChangeTime
)

// BehaviourSetBasicInfo sets stat of file or directory.
type BehaviourSetBasicInfo interface {
	SetBasicInfo(
		fs *FileSystemRef, file uintptr,
		flags SetBasicInfoFlags, attributes uint32,
		creationTime, lastAccessTime, lastWriteTime, changeTime uint64,
		fileInfo *FSP_FSCTL_FILE_INFO,
	) error
}

func delegateSetBasicInfo(
	fileSystem, fileContext uintptr,
	attributes uint32,
	creationTime, lastAccessTime, lastWriteTime, changeTime uint64,
	fileInfoAddr uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	var flags SetBasicInfoFlags
	if attributes != windows.INVALID_FILE_ATTRIBUTES {
		flags |= SetBasicInfoAttributes
	}
	if creationTime != 0 {
		flags |= SetBasicInfoCreationTime
	}
	if lastAccessTime != 0 {
		flags |= SetBasicInfoLastAccessTime
	}
	if lastWriteTime != 0 {
		flags |= SetBasicInfoLastWriteTime
	}
	if changeTime != 0 {
		flags |= SetBasicInfoChangeTime
	}
	return convertNTStatus(ref.setBasicInfo.SetBasicInfo(
		ref, fileContext, flags, attributes,
		creationTime, lastAccessTime, lastWriteTime, changeTime,
		(*FSP_FSCTL_FILE_INFO)(unsafe.Pointer(fileInfoAddr)),
	))
}

var go_delegateSetBasicInfo = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext uintptr,
	attributes uint32,
	creationTime, lastAccessTime, lastWriteTime, changeTime uint64,
	fileInfoAddr uintptr,
) uintptr {
	return uintptr(delegateSetBasicInfo(
		fileSystem, fileContext, attributes,
		creationTime, lastAccessTime, lastWriteTime, changeTime,
		fileInfoAddr,
	))
})

// BehaviourSetFileSize sets file's size or allocation size.
type BehaviourSetFileSize interface {
	SetFileSize(
		fs *FileSystemRef, file uintptr,
		newSize uint64, setAllocationSize bool,
		fileInfo *FSP_FSCTL_FILE_INFO,
	) error
}

func delegateSetFileSize(
	fileSystem, fileContext uintptr,
	newSize uint64, setAllocationSize uint8,
	fileInfoAddr uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	return convertNTStatus(ref.setFileSize.SetFileSize(
		ref, fileContext, newSize, setAllocationSize != 0,
		(*FSP_FSCTL_FILE_INFO)(unsafe.Pointer(fileInfoAddr)),
	))
}

var go_delegateSetFileSize = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext uintptr,
	newSize uint64, setAllocationSize uint8,
	fileInfoAddr uintptr,
) uintptr {
	return uintptr(delegateSetFileSize(
		fileSystem, fileContext,
		newSize, setAllocationSize,
		fileInfoAddr,
	))
})

// BehaviourCanDelete detects whether the file can be deleted.
type BehaviourCanDelete interface {
	CanDelete(
		fs *FileSystemRef, file uintptr, name string,
	) error
}

func delegateCanDelete(
	fileSystem, fileContext, filename uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	return convertNTStatus(ref.canDelete.CanDelete(
		ref, fileContext, utf16PtrToString(filename),
	))
}

var go_delegateCanDelete = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext, filename uintptr,
) uintptr {
	return uintptr(delegateCanDelete(
		fileSystem, fileContext, filename,
	))
})

// BehaviourRename renames a file or directory.
type BehaviourRename interface {
	Rename(
		fs *FileSystemRef, file uintptr,
		source, target string, replaceIfExist bool,
	) error
}

func delegateRename(
	fileSystem, fileContext uintptr,
	source, target uintptr, replaceIfExists uint8,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	return convertNTStatus(ref.rename.Rename(
		ref, fileContext,
		utf16PtrToString(source), utf16PtrToString(target),
		replaceIfExists != 0,
	))
}

var go_delegateRename = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext uintptr,
	source, target uintptr, replaceIfExists uint8,
) uintptr {
	return uintptr(delegateRename(
		fileSystem, fileContext,
		source, target, replaceIfExists,
	))
})

// BehaviourGetSecurity retrieves security descriptor by file.
type BehaviourGetSecurity interface {
	GetSecurity(
		fs *FileSystemRef, file uintptr,
	) (*windows.SECURITY_DESCRIPTOR, error)
}

func delegateGetSecurity(
	fileSystem, fileContext uintptr,
	securityDescAddr, securityDescSizeAddr uintptr,
) windows.NTStatus {
	size := (*uintptr)(unsafe.Pointer(securityDescSizeAddr))
	var bufferSize int
	if size != nil {
		bufferSize = int(*size)
		*size = 0
	}
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	sd, err := ref.getSecurity.GetSecurity(ref, fileContext)
	if err != nil {
		return convertNTStatus(err)
	}
	length := int(sd.Length())
	*size = uintptr(length)
	// XXX: though the API document says so, I haven't seen
	// under any circumstances will the security descriptor's
	// buffer address be NULL.
	if securityDescAddr != 0 {
		source := enforceBytePtr(uintptr(unsafe.Pointer(sd)), length)
		target := enforceBytePtr(securityDescAddr, bufferSize)
		if copy(target, source) < length {
			return windows.STATUS_BUFFER_OVERFLOW
		}
	}
	return windows.STATUS_SUCCESS
}

var go_delegateGetSecurity = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext uintptr,
	securityDescAddr, securityDescSizeAddr uintptr,
) uintptr {
	return uintptr(delegateGetSecurity(
		fileSystem, fileContext,
		securityDescAddr, securityDescSizeAddr,
	))
})

// BehaviourSetSecurity sets security descriptor by file.
type BehaviourSetSecurity interface {
	SetSecurity(
		fs *FileSystemRef, file uintptr,
		info windows.SECURITY_INFORMATION,
		desc *windows.SECURITY_DESCRIPTOR,
	) error
}

func delegateSetSecurity(
	fileSystem, fileContext uintptr,
	info windows.SECURITY_INFORMATION, securityDescSizeAddr uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	return convertNTStatus(ref.setSecurity.SetSecurity(
		ref, fileContext, info,
		(*windows.SECURITY_DESCRIPTOR)(unsafe.Pointer(
			securityDescSizeAddr))))
}

var go_delegateSetSecurity = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext uintptr,
	info windows.SECURITY_INFORMATION, securityDescSizeAddr uintptr,
) uintptr {
	return uintptr(delegateSetSecurity(
		fileSystem, fileContext,
		info, securityDescSizeAddr,
	))
})

var (
	deleteDirectoryBuffer  dllProc
	acquireDirectoryBuffer dllProc
	releaseDirectoryBuffer dllProc
	readDirectoryBuffer    dllProc
	fillDirectoryBuffer    dllProc
)

func init() {
	registerProc("FspFileSystemDeleteDirectoryBuffer", &deleteDirectoryBuffer)
	registerProc("FspFileSystemAcquireDirectoryBuffer", &acquireDirectoryBuffer)
	registerProc("FspFileSystemReleaseDirectoryBuffer", &releaseDirectoryBuffer)
	registerProc("FspFileSystemReadDirectoryBuffer", &readDirectoryBuffer)
	registerProc("FspFileSystemFillDirectoryBuffer", &fillDirectoryBuffer)
}

// DirBuffer is the directory buffer block which can be
// operated WinFSP's directory info API.
//
// To fill content into the buffer, one should try to acquire
// a DirBufferFiller, which is only acquired when there's no
// remaining content, or the user tells it to flush and reset.
type DirBuffer struct {
	ptr uintptr
}

// Delete the directory buffer.
func (buf *DirBuffer) Delete() {
	_, _ = deleteDirectoryBuffer.Call(
		uintptr(unsafe.Pointer(&buf.ptr)))
}

// ReadDirectory fills the read content into the buffer when
// there's no content remaining.
func (buf *DirBuffer) ReadDirectory(
	marker *uint16, buffer []byte,
) int {
	var bytesTransferred uint32
	slice := (*reflect.SliceHeader)(unsafe.Pointer(&buffer))
	_, _ = readDirectoryBuffer.Call(
		uintptr(unsafe.Pointer(&buf.ptr)),
		uintptr(unsafe.Pointer(marker)),
		slice.Data, uintptr(slice.Len),
		uintptr(unsafe.Pointer(&bytesTransferred)),
	)
	runtime.KeepAlive(marker)
	runtime.KeepAlive(buffer)
	return int(bytesTransferred)
}

// DirBufferFiller is the acquired filler of file system.
type DirBufferFiller struct {
	buf *DirBuffer
}

// Acquire the directory buffer filler when there has no
// content buffered, or it tells to reset the buffer.
//
// Unlike other interface, the acquisition may fail and
// the filler might be nil this case. The caller must
// judge whether there is error or there's no need to
// acquire the directory buffer yet.
func (buf *DirBuffer) Acquire(reset bool) (*DirBufferFiller, error) {
	var resetVal uintptr
	if reset {
		resetVal = uintptr(1)
	}
	acquireOk, err := acquireDirectoryBuffer.Call(
		uintptr(unsafe.Pointer(&buf.ptr)), resetVal,
		ntStatusPtr,
	)
	// BUG: microsoft's calling convention sets AL to 1
	// when the result is BOOLEAN, so we must only look
	// at the lowest bit of the digits then.
	if (uint8(acquireOk) != 1) || err != nil {
		return nil, err
	}
	return &DirBufferFiller{buf: buf}, nil
}

// Fill a directory entry into the directory filler.
//
// The iteration might also be stopped when the caller
// returns false, in thise case we should also terminate
// the iteration and copy the content out to the handler.
func (b *DirBufferFiller) Fill(
	name string, fileInfo *FSP_FSCTL_FILE_INFO,
) (bool, error) {
	utf16, err := windows.UTF16FromString(name)
	if err != nil {
		return false, err
	}
	if len(utf16) > 0 && utf16[len(utf16)-1] == 0 {
		// Prune the trailing NUL, since it is not need
		// while copying to the directory buffer.
		utf16 = utf16[:len(utf16)-1]
	}
	length := int(unsafe.Sizeof(FSP_FSCTL_DIR_INFO{}) +
		uintptr(len(utf16))*SIZEOF_WCHAR)
	alignedBuffer := make([]uint64, (length+7)/8)
	alignedAddr := uintptr(unsafe.Pointer(&alignedBuffer[0]))
	dirInfo := (*FSP_FSCTL_DIR_INFO)(unsafe.Pointer(alignedAddr))
	dirInfo.Size = uint16(length)
	if fileInfo != nil {
		dirInfo.FileInfo = *fileInfo
	}
	target := *((*[]uint16)(unsafe.Pointer(&reflect.SliceHeader{
		Data: alignedAddr + unsafe.Sizeof(FSP_FSCTL_DIR_INFO{}),
		Len:  len(utf16),
		Cap:  len(utf16),
	})))
	copy(target, utf16)
	copyOk, err := fillDirectoryBuffer.Call(
		uintptr(unsafe.Pointer(&b.buf.ptr)), alignedAddr,
		ntStatusPtr,
	)
	runtime.KeepAlive(alignedBuffer)
	// BUG: same bug as the acquire counterpart here.
	return uint8(copyOk) != 0, err
}

// Release the directory buffer filler.
func (b *DirBufferFiller) Release() {
	_, _ = releaseDirectoryBuffer.Call(
		uintptr(unsafe.Pointer(&b.buf.ptr)))
}

// BehaviourReadDirectoryRaw is the raw interface of read
// directory. Under most circumstances, the caller should
// implement BehaviourReadDirectory interface instead.
//
// For performance issue, the pattern and marker are not
// translated into go string.
type BehaviourReadDirectoryRaw interface {
	ReadDirectoryRaw(
		fs *FileSystemRef, file uintptr,
		pattern, marker *uint16, buf []byte,
	) (int, error)
}

func delegateReadDirectory(
	fileSystem, fileContext uintptr,
	pattern, marker *uint16,
	buf uintptr, length uint32, numRead *uint32,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	n, err := ref.readDirRaw.ReadDirectoryRaw(
		ref, fileContext, pattern, marker,
		enforceBytePtr(buf, int(length)))
	*numRead = uint32(n)
	return convertNTStatus(err)
}

var go_delegateReadDirectory = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext uintptr,
	pattern, marker *uint16,
	buf uintptr, length uint32, numRead *uint32,
) uintptr {
	return uintptr(delegateReadDirectory(
		fileSystem, fileContext,
		pattern, marker,
		buf, length, numRead,
	))
})

// BehaviourReadDirectoryOffset is a low-level interface
// for implementing reading of directories that is suitable
// for filesystems that can read directories with pagination
// implemented by an integer offset.
type BehaviourReadDirectoryOffset interface {
	ReadDirectoryOffset(
		fs *FileSystemRef, file uintptr,
		pattern *uint16, marker uint64, buf []byte,
	) (int, error)
}

type behaviourReadDirectoryOffset struct {
	readDirOffset BehaviourReadDirectoryOffset
}

func (d *behaviourReadDirectoryOffset) ReadDirectoryRaw(
	fs *FileSystemRef, file uintptr,
	pattern, marker *uint16, buf []byte,
) (int, error) {
	var offset uint64
	if marker != nil {
		offset = *(*uint64)(unsafe.Pointer(marker))
	}
	return d.readDirOffset.ReadDirectoryOffset(fs, file, pattern, offset, buf)
}

// BehaviourReadDirectory is the delegated interface which
// requires a translation from file descriptor and its
// dedicated directory buffer, alongside with occasionally
// called read directory call.
//
// The directory buffer allocated by the file system must be
// destroyed manually when the BehaviourBase.Close method
// has been called.
type BehaviourReadDirectory interface {
	GetOrNewDirBuffer(
		fileSystem *FileSystemRef, file uintptr,
	) (*DirBuffer, error)

	ReadDirectory(
		fs *FileSystemRef, file uintptr, pattern string,
		fill func(string, *FSP_FSCTL_FILE_INFO) (bool, error),
	) error
}

type behaviourReadDirectoryDelegate struct {
	readDir BehaviourReadDirectory
}

func (d *behaviourReadDirectoryDelegate) ReadDirectoryRaw(
	fs *FileSystemRef, file uintptr,
	pattern, marker *uint16, buf []byte,
) (int, error) {
	// XXX: This is literally identital to the WinFsp-Tutorial.
	// https://github.com/winfsp/winfsp/wiki/WinFsp-Tutorial#readdirectory
	dirBuf, err := d.readDir.GetOrNewDirBuffer(fs, file)
	if err != nil {
		return 0, err
	}
	filler, err := dirBuf.Acquire(marker == nil)
	if err != nil {
		return 0, err
	}
	if filler != nil {
		if err := func() error {
			defer filler.Release()
			var readPattern string
			if pattern != nil {
				readPattern = windows.UTF16PtrToString(pattern)
			}
			return d.readDir.ReadDirectory(
				fs, file, readPattern, filler.Fill)
		}(); err != nil {
			return 0, err
		}
	}
	return dirBuf.ReadDirectory(marker, buf), nil
}

// BehaviourGetDirInfoByName get directory information for a
// file or directory within a parent directory.
type BehaviourGetDirInfoByName interface {
	GetDirInfoByName(
		fs *FileSystemRef, parentDirFile uintptr,
		name string, dirInfo *FSP_FSCTL_DIR_INFO,
	) error
}

func delegateGetDirInfoByName(
	fileSystem, parentDirFile uintptr,
	fileName, dirInfoAddr uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	return convertNTStatus(ref.getDirInfoByName.GetDirInfoByName(
		ref, parentDirFile, utf16PtrToString(fileName),
		(*FSP_FSCTL_DIR_INFO)(unsafe.Pointer(dirInfoAddr)),
	))
}

var go_delegateGetDirInfoByName = syscall.NewCallbackCDecl(func(
	fileSystem, parentDirFile uintptr,
	fileName, dirInfoAddr uintptr,
) uintptr {
	return uintptr(delegateGetDirInfoByName(
		fileSystem, parentDirFile,
		fileName, dirInfoAddr,
	))
})

// BehaviourDeviceIoControl processes control code.
type BehaviourDeviceIoControl interface {
	DeviceIoControl(
		fs *FileSystemRef, file uintptr,
		code uint32, data []byte,
	) ([]byte, error)
}

func delegateDeviceIoControl(
	fileSystem, fileContext uintptr, controlCode uint32,
	inputBuffer uintptr, inputBufferLength uint32,
	outputBuffer uintptr, outputBufferLength uint32,
	bytesWritten *uint32,
) windows.NTStatus {
	*bytesWritten = 0
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	input := enforceBytePtr(inputBuffer, int(inputBufferLength))
	result, err := ref.deviceIoControl.DeviceIoControl(
		ref, fileContext, controlCode, input,
	)
	if err != nil {
		return convertNTStatus(err)
	}
	output := enforceBytePtr(outputBuffer, int(outputBufferLength))
	copied := copy(output, result)
	*bytesWritten = uint32(copied)
	if copied < len(output) {
		return windows.STATUS_BUFFER_OVERFLOW
	}
	return windows.STATUS_SUCCESS
}

var go_delegateDeviceIoControl = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext uintptr, controlCode uint32,
	inputBuffer uintptr, inputBufferLength uint32,
	outputBuffer uintptr, outputBufferLength uint32,
	bytesWritten *uint32,
) uintptr {
	return uintptr(delegateDeviceIoControl(
		fileSystem, fileContext, controlCode,
		inputBuffer, inputBufferLength,
		outputBuffer, outputBufferLength,
		bytesWritten,
	))
})

// BehaviourCreateEx creates file with extended attributes.
//
// Please notice this interface conflicts with BehaviourCreate
// and is prioritized over it.
type BehaviourCreateEx interface {
	CreateExWithExtendedAttribute(
		fs *FileSystemRef, name string,
		createOptions, grantedAccess, fileAttributes uint32,
		securityDescriptor *windows.SECURITY_DESCRIPTOR,
		extendedAttribute *FILE_FULL_EA_INFORMATION,
		allocationSize uint64, info *FSP_FSCTL_FILE_INFO,
	) (uintptr, error)

	CreateExWithReparsePointData(
		fs *FileSystemRef, name string,
		createOptions, grantedAccess, fileAttributes uint32,
		securityDescriptor *windows.SECURITY_DESCRIPTOR,
		extendedAttribute *REPARSE_DATA_BUFFER_GENERIC,
		allocationSize uint64, info *FSP_FSCTL_FILE_INFO,
	) (uintptr, error)
}

func delegateCreateEx(
	fileSystem, fileName uintptr,
	createOptions, grantedAccess, fileAttributes uint32,
	securityDescriptor uintptr, allocationSize uint64,
	extraBuffer uintptr, extraLength uint32, isReparse uint8,
	file *uintptr, fileInfoAddr uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	result, err := func() (uintptr, error) {
		if isReparse != 0 {
			return ref.createEx.CreateExWithReparsePointData(
				ref, utf16PtrToString(fileName),
				createOptions, grantedAccess, fileAttributes,
				(*windows.SECURITY_DESCRIPTOR)(
					unsafe.Pointer(securityDescriptor)),
				(*REPARSE_DATA_BUFFER_GENERIC)(
					unsafe.Pointer(extraBuffer)),
				allocationSize, (*FSP_FSCTL_FILE_INFO)(
					unsafe.Pointer(fileInfoAddr)),
			)
		} else {
			return ref.createEx.CreateExWithExtendedAttribute(
				ref, utf16PtrToString(fileName),
				createOptions, grantedAccess, fileAttributes,
				(*windows.SECURITY_DESCRIPTOR)(
					unsafe.Pointer(securityDescriptor)),
				(*FILE_FULL_EA_INFORMATION)(
					unsafe.Pointer(extraBuffer)),
				allocationSize, (*FSP_FSCTL_FILE_INFO)(
					unsafe.Pointer(fileInfoAddr)),
			)
		}
	}()
	if err != nil {
		return convertNTStatus(err)
	}
	*file = result
	return windows.STATUS_SUCCESS
}

var go_delegateCreateEx = syscall.NewCallbackCDecl(func(
	fileSystem, fileName uintptr,
	createOptions, grantedAccess, fileAttributes uint32,
	securityDescriptor uintptr, allocationSize uint64,
	extraBuffer uintptr, extraLength uint32, isReparse uint8,
	file *uintptr, fileInfoAddr uintptr,
) uintptr {
	return uintptr(delegateCreateEx(
		fileSystem, fileName,
		createOptions, grantedAccess, fileAttributes,
		securityDescriptor, allocationSize,
		extraBuffer, extraLength, isReparse,
		file, fileInfoAddr,
	))
})

var (
	fileSystemResolveReparsePoints dllProc
)

func init() {
	registerProc("FspFileSystemResolveReparsePoints", &fileSystemResolveReparsePoints)
}

// BehaviourDeleteReparsePoint deletes a reparse point.
type BehaviourDeleteReparsePoint interface {
	DeleteReparsePoint(
		fs *FileSystemRef, file uintptr, name string,
		buffer []byte,
	) error
}

func delegateDeleteReparsePoint(
	fileSystem, fileContext, fileName uintptr,
	buffer, size uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	return convertNTStatus(ref.deleteReparsePoint.DeleteReparsePoint(
		ref, fileContext, utf16PtrToString(fileName),
		enforceBytePtr(buffer, int(size)),
	))
}

var go_delegateDeleteReparsePoint = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext, fileName uintptr,
	buffer, size uintptr,
) uintptr {
	return uintptr(delegateDeleteReparsePoint(
		fileSystem, fileContext, fileName,
		buffer, size,
	))
})

// BehaviourGetReparsePoint gets a reparse point.
type BehaviourGetReparsePoint interface {
	GetReparsePoint(
		fs *FileSystemRef, file uintptr, name string,
		buffer []byte,
	) (int, error)
}

func delegateGetReparsePoint(
	fileSystem, fileContext, fileName uintptr,
	buffer uintptr, size *uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	bufferSize := int(*size)
	usedBytes, err := ref.getReparsePoint.GetReparsePoint(
		ref, fileContext, utf16PtrToString(fileName),
		enforceBytePtr(buffer, bufferSize),
	)
	if err != nil {
		return convertNTStatus(err)
	}
	*size = uintptr(usedBytes)
	return windows.STATUS_SUCCESS
}

var go_delegateGetReparsePoint = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext, fileName uintptr,
	buffer uintptr, size *uintptr,
) uintptr {
	return uintptr(delegateGetReparsePoint(
		fileSystem, fileContext, fileName,
		buffer, size,
	))
})

// BehaviourGetReparsePoint gets a reparse point.
type BehaviourGetReparsePointByName interface {
	GetReparsePointByName(
		fs *FileSystemRef, name string, isDirectory bool,
		buffer []byte,
	) (int, error)
}

func delegateGetReparsePointByName(
	fileSystem, context, fileName uintptr,
	isDirectory uint8, buffer uintptr, size *uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	if ref.getReparsePointByName == nil {
		return windows.STATUS_INVALID_DEVICE_REQUEST
	}
	var bufferSize int
	if size != nil {
		bufferSize = int(*size)
	} else {
		bufferSize = 0
	}
	usedBytes, err := ref.getReparsePointByName.GetReparsePointByName(
		ref, utf16PtrToString(fileName), isDirectory != 0,
		enforceBytePtr(buffer, bufferSize),
	)
	if err != nil {
		return convertNTStatus(err)
	}
	if size != nil {
		*size = uintptr(usedBytes)
	}
	return windows.STATUS_SUCCESS
}

var go_delegateGetReparsePointByName = syscall.NewCallbackCDecl(func(
	fileSystem, context, fileName uintptr,
	isDirectory uint8, buffer uintptr, size *uintptr,
) uintptr {
	return uintptr(delegateGetReparsePointByName(
		fileSystem, context, fileName,
		isDirectory, buffer, size,
	))
})

func delegateResolveReparsePoints(
	fileSystem, fileName uintptr,
	reparsePointIndex uint32, resolveLastPathComponent uint8,
	ioStatus, buffer uintptr, size *uintptr,
) windows.NTStatus {
	// Call the WinFSP API
	err := fileSystemResolveReparsePoints.CallStatus(
		fileSystem,
		go_delegateGetReparsePointByName,
		uintptr(0),
		fileName,
		uintptr(reparsePointIndex),
		uintptr(resolveLastPathComponent),
		ioStatus,
		buffer,
		uintptr(unsafe.Pointer(size)),
	)
	if err != nil {
		return convertNTStatus(err) // from error-boxed NTStatus -> NTStatus
	}
	return windows.STATUS_SUCCESS
}

var go_delegateResolveReparsePoints = syscall.NewCallbackCDecl(func(
	fileSystem, fileName uintptr,
	reparsePointIndex uint32, resolveLastPathComponent uint8,
	ioStatus, buffer uintptr, size *uintptr,
) uintptr {
	return uintptr(delegateResolveReparsePoints(
		fileSystem, fileName,
		reparsePointIndex, resolveLastPathComponent,
		ioStatus, buffer, size,
	))
})

// BehaviourSetReparsePoint sets a reparse point.
type BehaviourSetReparsePoint interface {
	SetReparsePoint(
		fs *FileSystemRef, file uintptr, name string,
		buffer []byte,
	) error
}

func delegateSetReparsePoint(
	fileSystem, fileContext, fileName uintptr,
	buffer, size uintptr,
) windows.NTStatus {
	ref := loadFileSystemRef(fileSystem)
	if ref == nil {
		return ntStatusNoRef
	}
	return convertNTStatus(ref.setReparsePoint.SetReparsePoint(
		ref, fileContext, utf16PtrToString(fileName),
		enforceBytePtr(buffer, int(size)),
	))
}

var go_delegateSetReparsePoint = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext, fileName uintptr,
	buffer, size uintptr,
) uintptr {
	return uintptr(delegateSetReparsePoint(
		fileSystem, fileContext, fileName,
		buffer, size,
	))
})

type option struct {
	caseSensitive            bool
	casePreserveNames        bool
	volumePrefix             string
	fileSystemName           string
	passPattern              bool
	attributes               uint32
	creationTime             time.Time
	debug                    bool
	sectorSize               uint16
	sectorsPerAllocationUnit uint16
}

func newOption() *option {
	return &option{
		caseSensitive:            false,
		volumePrefix:             "",
		fileSystemName:           "WinFSP",
		creationTime:             time.Now(),
		sectorSize:               512,
		sectorsPerAllocationUnit: 1,
	}
}

// Option is the options that could be passed to mount.
type Option func(*option)

// Attributes can be used to apply additional FspFSAttribute
// attributes to the filesystem.
func Attributes(value uint32) Option {
	return func(o *option) {
		o.attributes |= value
	}
}

// CaseSensitive is used to indicate whether the underlying
// filesystem can be distinguied case sensitively.
//
// This value should be set depending on your filesystem's
// implementation. On windows, it is very likely that the
// filesystem is case insensitive, so we set this value to
// false by default.
func CaseSensitive(value bool) Option {
	return func(o *option) {
		o.caseSensitive = value
	}
}

// CasePreserveNames is used to indicate whether the underlying
// filesystem preserve cases when storing file names.
//
// This value should be set depending on your filesystem's
// implementation.  On windows, it is very likely that the
// filesystem does not preserve cases while storing names,
// so we set this value to false by default.
func CasePreserveNames(value bool) Option {
	return func(o *option) {
		o.casePreserveNames = value
	}
}

// Debug controls whether WinFSP's debug logging will be
// emitted for this file system. The destination for the debug
// logging can be set using the DebugLogSetHandle function.
func Debug(value bool) Option {
	return func(o *option) {
		o.debug = value
	}
}

// VolumePrefix sets the volume prefix on mounting.
//
// Specifying volume prefix will turn the filesystem into
// a network device instead of the disk one.
func VolumePrefix(value string) Option {
	return func(o *option) {
		o.volumePrefix = value
	}
}

// FileSystemName sets the file system's type for display.
func FileSystemName(value string) Option {
	return func(o *option) {
		o.fileSystemName = value
	}
}

// CreationTime sets the volume creation time explicitly,
// instead of using the timestamp of calling mount.
func CreationTime(value time.Time) Option {
	return func(o *option) {
		o.creationTime = value
	}
}

// PassPattern specifies whether the pattern for read
// directory should be passed.
func PassPattern(value bool) Option {
	return func(o *option) {
		o.passPattern = value
	}
}

// SectorSize sets the sector size and sectors per allocation unit
// for the volume.
func SectorSize(sectorSize, sectorsPerAllocationUnit uint16) Option {
	return func(o *option) {
		o.sectorSize = sectorSize
		o.sectorsPerAllocationUnit = sectorsPerAllocationUnit
	}
}

// Options is used to aggregate a bundle of options.
func Options(opts ...Option) Option {
	return func(o *option) {
		for _, opt := range opts {
			opt(o)
		}
	}
}

// BehaviourDefaultOptions allows the implementors
// of BehaviourBase to specify a set of default
// winfsp.Options with respect to their own state.
//
// The user may still override the options, but
// at their own risks.
type BehaviourDefaultOptions interface {
	DefaultOptions() []Option
}

const (
	fspNetDeviceName  = "WinFSP.Net"
	fspDiskDeviceName = "WinFSP.Disk"
)

var (
	fileSystemCreate dllProc
	fileSystemDelete dllProc
	setMountPoint    dllProc
	startDispatcher  dllProc
	stopDispatcher   dllProc
	setDebugLogF     dllProc
)

func init() {
	registerProc("FspFileSystemCreate", &fileSystemCreate)
	registerProc("FspFileSystemDelete", &fileSystemDelete)
	registerProc("FspFileSystemSetMountPoint", &setMountPoint)
	registerProc("FspFileSystemStartDispatcher", &startDispatcher)
	registerProc("FspFileSystemStopDispatcher", &stopDispatcher)
	registerProc("FspFileSystemSetDebugLogF", &setDebugLogF)
}

// Mount attempts to mount a file system to specified mount
// point, returning the handle to the real filesystem.
func Mount(
	fs BehaviourBase, mountpoint string, opts ...Option,
) (*FileSystem, error) {
	if fs == nil {
		return nil, errors.New("invalid nil fs parameter")
	}
	if err := tryLoadWinFSP(); err != nil {
		return nil, err
	}
	option := newOption()
	if inner, ok := fs.(BehaviourDefaultOptions); ok {
		Options(inner.DefaultOptions()...)(option)
	}
	Options(opts...)(option)
	created := false

	// Place the reference map right now.
	result := &FileSystem{}
	fileSystemRef := &result.FileSystemRef
	fileSystemAddr := uintptr(unsafe.Pointer(fileSystemRef))
	_, loaded := refMap.LoadOrStore(fileSystemAddr, fileSystemRef)
	if loaded {
		return nil, errors.New("out of memory")
	}
	defer func() {
		if !created {
			refMap.Delete(fileSystemAddr)
		}
	}()
	attributes := option.attributes
	if option.caseSensitive {
		attributes |= FspFSAttributeCaseSensitive
	}
	if option.casePreserveNames {
		attributes |= FspFSAttributeCasePreservedNames
	}
	attributes |= FspFSAttributeUnicodeOnDisk
	attributes |= FspFSAttributePersistentAcls
	attributes |= FspFSAttributeFlushAndPurgeOnCleanup
	if option.passPattern {
		attributes |= FspFSAttributePassQueryDirectoryPattern
	}
	attributes |= FspFSAttributeUmFileContextIsUserContext2

	// Intepret the behaviours to convert interface.
	//
	// XXX: we will also need to store the fileSystemOps into
	// the fileSystemRef, since the FspFileSystemCreate will
	// create reference to this object, which might be GC-ed
	// and reused by the golang's runtime.
	fileSystemOps := &FSP_FILE_SYSTEM_INTERFACE{}
	fileSystemRef.base = fs
	fileSystemRef.fileSystemOps = fileSystemOps
	fileSystemOps.Open = go_delegateOpen
	fileSystemOps.Close = go_delegateClose
	if inner, ok := fs.(BehaviourGetVolumeInfo); ok {
		fileSystemRef.getVolumeInfo = inner
		fileSystemOps.GetVolumeInfo = go_delegateGetVolumeInfo
	}
	if inner, ok := fs.(BehaviourSetVolumeLabel); ok {
		fileSystemRef.setVolumeLabel = inner
		fileSystemOps.SetVolumeLabel = go_delegateSetVolumeLabel
	}
	if inner, ok := fs.(BehaviourGetSecurityByName); ok {
		fileSystemRef.getSecurityByName = inner
		fileSystemOps.GetSecurityByName = go_delegateGetSecurityByName
	}
	if inner, ok := fs.(BehaviourCreateEx); ok {
		fileSystemRef.createEx = inner
		fileSystemOps.CreateEx = go_delegateCreateEx
	} else if inner, ok := fs.(BehaviourCreate); ok {
		fileSystemRef.create = inner
		fileSystemOps.Create = go_delegateCreate
	}
	if inner, ok := fs.(BehaviourOverwrite); ok {
		fileSystemRef.overwrite = inner
		fileSystemOps.Overwrite = go_delegateOverwrite
	}
	if inner, ok := fs.(BehaviourCleanup); ok {
		fileSystemRef.cleanup = inner
		fileSystemOps.Cleanup = go_delegateCleanup
	}
	if inner, ok := fs.(BehaviourRead); ok {
		fileSystemRef.read = inner
		fileSystemOps.Read = go_delegateRead
	}
	if inner, ok := fs.(BehaviourWrite); ok {
		fileSystemRef.write = inner
		fileSystemOps.Write = go_delegateWrite
	}
	if inner, ok := fs.(BehaviourFlush); ok {
		fileSystemRef.flush = inner
		fileSystemOps.Flush = go_delegateFlush
	}
	if inner, ok := fs.(BehaviourGetFileInfo); ok {
		fileSystemRef.getFileInfo = inner
		fileSystemOps.GetFileInfo = go_delegateGetFileInfo
	}
	if inner, ok := fs.(BehaviourDeviceIoControl); ok {
		fileSystemRef.deviceIoControl = inner
		fileSystemOps.Control = go_delegateDeviceIoControl
	}
	if inner, ok := fs.(BehaviourDeleteReparsePoint); ok {
		fileSystemRef.deleteReparsePoint = inner
		fileSystemOps.DeleteReparsePoint = go_delegateDeleteReparsePoint
	}
	if inner, ok := fs.(BehaviourGetReparsePoint); ok {
		fileSystemRef.getReparsePoint = inner
		fileSystemOps.GetReparsePoint = go_delegateGetReparsePoint
	}
	if inner, ok := fs.(BehaviourGetReparsePointByName); ok {
		attributes |= FspFSAttributeReparsePoints
		fileSystemRef.getReparsePointByName = inner
		fileSystemOps.ResolveReparsePoints = go_delegateResolveReparsePoints
	}
	if inner, ok := fs.(BehaviourSetReparsePoint); ok {
		fileSystemRef.setReparsePoint = inner
		fileSystemOps.SetReparsePoint = go_delegateSetReparsePoint
	}
	if inner, ok := fs.(BehaviourSetBasicInfo); ok {
		fileSystemRef.setBasicInfo = inner
		fileSystemOps.SetBasicInfo = go_delegateSetBasicInfo
	}
	if inner, ok := fs.(BehaviourSetFileSize); ok {
		fileSystemRef.setFileSize = inner
		fileSystemOps.SetFileSize = go_delegateSetFileSize
	}
	if inner, ok := fs.(BehaviourCanDelete); ok {
		fileSystemRef.canDelete = inner
		fileSystemOps.CanDelete = go_delegateCanDelete
	}
	if inner, ok := fs.(BehaviourRename); ok {
		fileSystemRef.rename = inner
		fileSystemOps.Rename = go_delegateRename
	}
	if inner, ok := fs.(BehaviourGetSecurity); ok {
		fileSystemRef.getSecurity = inner
		fileSystemOps.GetSecurity = go_delegateGetSecurity
	}
	if inner, ok := fs.(BehaviourSetSecurity); ok {
		fileSystemRef.setSecurity = inner
		fileSystemOps.SetSecurity = go_delegateSetSecurity
	}
	if inner, ok := fs.(BehaviourReadDirectoryOffset); ok {
		attributes |= FspFSAttributeDirectoryMarkerAsNextOffset
		fileSystemRef.readDirRaw = &behaviourReadDirectoryOffset{
			readDirOffset: inner,
		}
		fileSystemOps.ReadDirectory = go_delegateReadDirectory
	} else if inner, ok := fs.(BehaviourReadDirectoryRaw); ok {
		fileSystemRef.readDirRaw = inner
		fileSystemOps.ReadDirectory = go_delegateReadDirectory
	} else if inner, ok := fs.(BehaviourReadDirectory); ok {
		fileSystemRef.readDirRaw = &behaviourReadDirectoryDelegate{
			readDir: inner,
		}
		fileSystemOps.ReadDirectory = go_delegateReadDirectory
	}
	if inner, ok := fs.(BehaviourGetDirInfoByName); ok {
		fileSystemRef.getDirInfoByName = inner
		fileSystemOps.GetDirInfoByName = go_delegateGetDirInfoByName
	}
	if inner, ok := fs.(BehaviourDeviceIoControl); ok {
		fileSystemRef.deviceIoControl = inner
		fileSystemOps.Control = go_delegateDeviceIoControl
	}

	// Convert the file system names into their wchar types.
	convertError := func(err error, content string) error {
		return errors.Wrapf(err, "string %q convert utf16", content)
	}
	utf16Prefix, err := windows.UTF16FromString(option.volumePrefix)
	if err != nil {
		return nil, convertError(err, option.volumePrefix)
	}
	utf16Name, err := windows.UTF16FromString(option.fileSystemName)
	if err != nil {
		return nil, convertError(err, option.fileSystemName)
	}
	utf16MountPoint, err := windows.UTF16PtrFromString(mountpoint)
	if err != nil {
		return nil, convertError(err, mountpoint)
	}
	driverName := fspDiskDeviceName
	if option.volumePrefix != "" {
		driverName = fspNetDeviceName
	}
	utf16Driver, err := windows.UTF16PtrFromString(driverName)
	if err != nil {
		return nil, convertError(err, driverName)
	}

	// Convert and file the volume parameters for mounting.
	volumeParams := &FSP_FSCTL_VOLUME_PARAMS_V1{}
	const sizeOfVolumeParamsV1 = uint16(unsafe.Sizeof(
		FSP_FSCTL_VOLUME_PARAMS_V1{}))
	volumeParams.SizeOfVolumeParamsV1 = sizeOfVolumeParamsV1
	volumeParams.SectorSize = option.sectorSize
	volumeParams.SectorsPerAllocationUnit = option.sectorsPerAllocationUnit
	nowFiletime := syscall.NsecToFiletime(
		option.creationTime.UnixNano())
	volumeParams.VolumeCreationTime =
		*(*uint64)(unsafe.Pointer(&nowFiletime))
	volumeParams.FileSystemAttribute = attributes
	copy(volumeParams.Prefix[:], utf16Prefix)
	copy(volumeParams.FileSystemName[:], utf16Name)

	// Attempt to create the file system now.
	err = fileSystemCreate.CallStatus(
		uintptr(unsafe.Pointer(utf16Driver)),
		uintptr(unsafe.Pointer(volumeParams)),
		uintptr(unsafe.Pointer(fileSystemOps)),
		uintptr(unsafe.Pointer(&result.fileSystem)),
	)
	runtime.KeepAlive(utf16Driver)
	runtime.KeepAlive(volumeParams)
	runtime.KeepAlive(fileSystemOps)
	if err != nil {
		return nil, errors.Wrap(err, "create file system")
	}
	defer func() {
		if !created {
			_, _ = fileSystemDelete.Call(
				uintptr(unsafe.Pointer(result.fileSystem)))
		}
	}()
	result.fileSystem.UserContext = fileSystemAddr

	if option.debug {
		// Set debug log level to maximum for debug output
		_, err = setDebugLogF.Call(
			uintptr(unsafe.Pointer(result.fileSystem)),
			uintptr(math.MaxUint32),
		)
		if err == syscall.Errno(0) {
			err = nil
		}
		if err != nil {
			return nil, errors.Wrap(err, "FspFileSystemSetDebugLogF")
		}
	}

	// Attempt to mount the file system at mount point.
	err = setMountPoint.CallStatus(
		uintptr(unsafe.Pointer(result.fileSystem)),
		uintptr(unsafe.Pointer(utf16MountPoint)),
	)
	runtime.KeepAlive(utf16MountPoint)
	if err != nil {
		return nil, errors.Wrap(err, "mount file system")
	}

	// Attempt to start the file system dispatcher.
	err = startDispatcher.CallStatus(
		uintptr(unsafe.Pointer(result.fileSystem)), uintptr(0),
	)
	if err != nil {
		return nil, errors.Wrap(err, "start dispatcher")
	}
	defer func() {
		if !created {
			_, _ = stopDispatcher.Call(
				uintptr(unsafe.Pointer(result.fileSystem)))
		}
	}()
	created = true
	return result, nil
}

// Unmount destroy the created file system.
func (f *FileSystem) Unmount() {
	fileSystem := uintptr(unsafe.Pointer(f.fileSystem))
	_, _ = stopDispatcher.Call(fileSystem)
	_, _ = fileSystemDelete.Call(fileSystem)
}
