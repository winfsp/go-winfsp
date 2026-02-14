// Package gofs aims at providing a simple but working
// Golang filesystem implementation for WinFSP.
//
// The filesystem supports only file and directory, both
// of them can be opened by OpenFile operation, returning
// a File interface. The File interface should supports
// only read, write (append or random), close, seek, sync,
// readdir, truncate and stat operations.
//
// On the filesystem level, it supports Stat, OpenFile,
// Mkdir, Remove and Rename operations.
//
// For simplicity, the filesystem is always case sensitive.
//
// This makes it works even if the underlying filesystem
// is backed by a Window's native directory through the
// language interfaces by Golang.
package gofs
