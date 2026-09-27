package winfsp

import "syscall"

// Callbacks taking 64-bit arguments by value. The x86 cdecl
// calling convention passes such an argument in two consecutive
// 32-bit stack slots, low word first, while syscall.NewCallbackCDecl
// only accepts arguments up to the size of uintptr. So each 64-bit
// argument is received as two uint32 halves and recombined here.

func makeUint64(lo, hi uint32) uint64 {
	return uint64(hi)<<32 | uint64(lo)
}

var go_delegateCreate = syscall.NewCallbackCDecl(func(
	fileSystem, fileName uintptr,
	createOptions, grantedAccess, fileAttributes uint32,
	securityDescriptor uintptr, allocationSizeLo, allocationSizeHi uint32,
	file *uintptr, fileInfoAddr uintptr,
) uintptr {
	return uintptr(delegateCreate(
		fileSystem, fileName,
		createOptions, grantedAccess, fileAttributes,
		securityDescriptor, makeUint64(allocationSizeLo, allocationSizeHi),
		file, fileInfoAddr,
	))
})

var go_delegateOverwrite = syscall.NewCallbackCDecl(func(
	fileSystem, file uintptr,
	attributes uint32, replaceAttributes uint8,
	allocationSizeLo, allocationSizeHi uint32, fileInfoAddr uintptr,
) uintptr {
	return uintptr(delegateOverwrite(
		fileSystem, file,
		attributes, replaceAttributes,
		makeUint64(allocationSizeLo, allocationSizeHi), fileInfoAddr,
	))
})

var go_delegateRead = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext, buffer uintptr,
	offsetLo, offsetHi uint32, length uint32, bytesRead *uint32,
) uintptr {
	return uintptr(delegateRead(
		fileSystem, fileContext, buffer,
		makeUint64(offsetLo, offsetHi), length, bytesRead,
	))
})

var go_delegateWrite = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext, buffer uintptr,
	offsetLo, offsetHi uint32, length uint32,
	writeToEndOfFile, constrainedIo uint8,
	bytesWritten *uint32, fileInfoAddr uintptr,
) uintptr {
	return uintptr(delegateWrite(
		fileSystem, fileContext, buffer,
		makeUint64(offsetLo, offsetHi), length,
		writeToEndOfFile, constrainedIo,
		bytesWritten, fileInfoAddr,
	))
})

var go_delegateSetBasicInfo = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext uintptr,
	attributes uint32,
	creationTimeLo, creationTimeHi uint32,
	lastAccessTimeLo, lastAccessTimeHi uint32,
	lastWriteTimeLo, lastWriteTimeHi uint32,
	changeTimeLo, changeTimeHi uint32,
	fileInfoAddr uintptr,
) uintptr {
	return uintptr(delegateSetBasicInfo(
		fileSystem, fileContext, attributes,
		makeUint64(creationTimeLo, creationTimeHi),
		makeUint64(lastAccessTimeLo, lastAccessTimeHi),
		makeUint64(lastWriteTimeLo, lastWriteTimeHi),
		makeUint64(changeTimeLo, changeTimeHi),
		fileInfoAddr,
	))
})

var go_delegateSetFileSize = syscall.NewCallbackCDecl(func(
	fileSystem, fileContext uintptr,
	newSizeLo, newSizeHi uint32, setAllocationSize uint8,
	fileInfoAddr uintptr,
) uintptr {
	return uintptr(delegateSetFileSize(
		fileSystem, fileContext,
		makeUint64(newSizeLo, newSizeHi), setAllocationSize,
		fileInfoAddr,
	))
})

var go_delegateCreateEx = syscall.NewCallbackCDecl(func(
	fileSystem, fileName uintptr,
	createOptions, grantedAccess, fileAttributes uint32,
	securityDescriptor uintptr, allocationSizeLo, allocationSizeHi uint32,
	extraBuffer uintptr, extraLength uint32, isReparse uint8,
	file *uintptr, fileInfoAddr uintptr,
) uintptr {
	return uintptr(delegateCreateEx(
		fileSystem, fileName,
		createOptions, grantedAccess, fileAttributes,
		securityDescriptor, makeUint64(allocationSizeLo, allocationSizeHi),
		extraBuffer, extraLength, isReparse,
		file, fileInfoAddr,
	))
})
