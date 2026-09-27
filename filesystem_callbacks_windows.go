//go:build !386

package winfsp

import "syscall"

// Callbacks taking 64-bit arguments by value. See
// filesystem_callbacks_windows_386.go for the 32-bit variants.

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

var go_delegateRead = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext, buffer uintptr,
	offset uint64, length uint32, bytesRead *uint32,
) uintptr {
	return uintptr(delegateRead(
		fileSystem, fileContext, buffer,
		offset, length, bytesRead,
	))
})

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
