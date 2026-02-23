// Package gofs aims at providing a simple but working
// Golang file system implementation for WinFSP.
//
// The file system supports only file and directory, both
// of them can be opened by OpenFile operation, returning
// a file interface. The File interface should supports
// only read, write (append or random), close, seek, sync,
// readdir, truncate and stat operations.
//
// On the filesystem level, it supports Stat, OpenFile,
// Mkdir, Remove and Rename operations.
//
// The filesystem can be either case-sensitive or
// case-insensitive. By default, it's case-sensitive.
// It can be switched to case-insensitive by specifying
// `gofs.WithCaseInsensitive(true)`. When it is
// case-insensitive, the implementor must support
// opening file with ignored cases, while preserving
// the cases when the file was created.
//
// This makes it works even if the underlying file system
// is backed by a Window's native directory through the
// language interfaces by Golang.
package gofs
