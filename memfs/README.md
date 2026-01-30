# memfs

This is an in-memory WinFSP filesystem. It's placed
under the root of the repository because we want to
use it in the test case.

To run this filesystem, first run from the repository
root that:

```bash
GOOS=windows go build -o memfs.exe ./examples/memfs/cmd/main.go
```

And then run the following command to mount it to `X:`:

```
memfs.exe -m X:
```

Then the filesystem is accessible from `X:`.

## Overview

This `memfs` aims at creating a minimalist in-memory
filesystem that supports only files and directories:

- Files hold actual data, and are represented by
  `memfs.memFile`.
- Directories hold files and subdirectories, and
  are represented by `memfs.memDir`. The files and
  directories are *held* as **dentries**, which are
  represented by `memfs.memItem`.

If you compare this example to the
[`examples/passthrough`](https://github.com/winfsp/go-winfsp/blob/master/examples/passthrough),
you will find this one is much complexer. This is
because `examples/passthrough` has a backing native
Windows filesystem, while `memfs` has not. Multiple
Windows application may access files and directory,
and we will have to do **concurrency control** ourself,
that is:

1. Control how multiple applications will modify the
   file hierarchy.
2. Control how multiple applications will modify the
   content of files.
3. Control how multiple applications will modify the
   metadata of files and directories.

For the file hierarchy, we linearize all file requests
by `memfs.MemFS.mtx`, since this is the simplest way.
For the file content and metadata, we also linearize
their requests by `memfs.memFile.dataMtx` and
`memfs.memItem.metaMtx`.

Some operations might involve multiple locks, and
will be prone to deadlocking if not treated carefully.
We prevent deadlock by:

1. Locks of different kinds must be acquired in a
   **fixed** order. In our case, it is
   `memfs.MemFS.mtx` > `memfs.memFile.dataMtx` > `memfs.memItem.metaMtx`.
2. For each kind of lock, there must be **only one**
   object tied to that lock. If it can't be achieved,
   one must resort to broader locking.

For the point #2, one example is renaming / moving
files. One might think we can create a dentry-lock
`memfs.memDir.dentsMtx` when modifying directories,
however this is prone to dead-locking: consider two
directories, `A` and `B`, and two concurrent renaming
requests, moving `A/a` to `B/a` and moving `B/b` to
`B/a`. Without loss of generality, let the moving
operation be implemented as acquiring `dentsMtx` of
the source directory, and then the target directory.
Then there will be a deadlock if the first request
acquires `A.dentsMtx` and waits for `B.dentsMtx`,
while the second request acquires `B.dentsMtx` and
waits for `A.dentsMtx`. Therefore, we resort to
locking `memfs.MemFS.mtx` for renaming / moving files.
The principle is applicable to other operations.