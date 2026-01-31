# go-winfsp

This is a [Go](https://golang.org) binding for the native
[WinFSP](https://github.com/winfsp/winfsp) API, which lets
you implement a Windows native filesystem based
on the WinFSP framework.

There's also another Go binding for WinFSP named
[cgofuse](https://github.com/winfsp/cgofuse), which is based
on WinFSP's FUSE compatibility layer API instead of the native
one. However, I've found it problematic when implementing a
filesystem backed by a Windows native filesystem, due to
the semantic differences between POSIX style file API and Windows'
file API. This is the major motivation for writing this package.

The go-winfsp binding should offer a Windows-specific but
seamless API to implement a filesystem. However, callers
need not worry about adapting to different platform's API,
since the API also provides a Go standard library style
interface for describing a filesystem, and the adaption
should be a breeze.

## Getting Start

You need to install the [WinFSP](https://github.com/winfsp/winfsp) first.

There're mainly two layers in go-winfsp:

- The **Binding** layer, which is located at
  [`github.com/winfsp/go-winfsp`](https://pkg.go.dev/github.com/winfsp/go-winfsp?GOOS=windows)
  and offers a native WinFSP API Go binding.
- The **Filesystem** layer, which is located at
  [`github.com/winfsp/go-winfsp/gofs`](https://pkg.go.dev/github.com/winfsp/go-winfsp/gofs?GOOS=windows)
  and offers support for adapting a `io/fs`-like Go filesystem
  to an accessible WinFSP filesystem.

It's recommended to start with the filesystem layer. A living
filesystem is tangible and easier to observe, isn't it?

For example, we can write a passthrough filesystem, which
"mounts" a directory to a disk:

```go
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
```

This creates a minimalist filesystem by
prepending the requesting file path with a
directory prefix, and then delegate the file
operations to the underlying `os.File`.

The filesystem can be later mounted 
at specified `mountpoint` by:

```go
//go:build windows

import (
	"github.com/winfsp/go-winfsp"
	"github.com/winfsp/go-winfsp/gofs"
)

ptfs, err := winfsp.Mount(gofs.New(&passthrough.Passthrough{Dir: dir}), mountpoint)
```

I've added some command line support to
create a complete runnable example.
The full source code is accessible at
[`examples/passthrough`](https://github.com/winfsp/go-winfsp/blob/master/examples/passthrough).

To run the filesystem, you can clone
this repository, and then build
`passthrough.exe` executable by running
from the repository root directory that:

```bash
GOOS=windows go build -o passthrough.exe ./examples/passthrough/cmd/main.go
```

Now you can mount `C:\some-directory` to disk `X:` by

```cmd
passthrough.exe -m X: C:\some-directory
```

You may try performing file operations in `X:`, and see
how it will be reflected on the `C:\some-directory`.

As the next step, there're also other examples lying
around, e.g.
[`memfs`](https://github.com/winfsp/go-winfsp/blob/master/memfs).
One should also refer to them before implementing
more complex filesystems.