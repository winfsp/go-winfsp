package treelock

import (
	"math"
	"path"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"
)

// node represents a single node page in the
// tree lock. It offers the starting point
// for acquiring a lock or querying path.
//
// A node is nullish, that is, every method
// of node must handle the case of nil node.
// This is due to we would allow path locking
// the root node, and the parent path of the
// root is nil, which is always read-lockable.
//
// Every operation of the node **must** hold
// the mutex of the tree locker.
type node struct {
	rc       uint64
	name     string
	parent   *node
	children map[string]*node
	exile    bool

	readers int64
	waitCh  chan struct{}
}

var childMapPool = &sync.Pool{
	New: func() any {
		return make(map[string]*node)
	},
}

type TreeLocker struct {
	mtx  sync.Mutex
	root *node
}

func New() *TreeLocker {
	return &TreeLocker{
		root: &node{
			name:   "",
			parent: nil,
		},
	}
}

// allocClean gets or allocates the nodes in
// the tree recursively.
func (tl *TreeLocker) allocClean(p string) *node {
	if p == "" || p == "." || p == "/" {
		return tl.root
	}
	dir, base := path.Split(p)
	dir = path.Clean(dir)
	// assert base != ""
	if base == "" {
		panic("base name is empty")
	}
	dirNode := tl.allocClean(dir)
	if dirNode == nil {
		panic("unexpected nil dirNode")
	}
	if dirNode.children == nil {
		dirNode.children = childMapPool.Get().(map[string]*node)
	}
	baseNode, ok := dirNode.children[base]
	if !ok {
		baseNode = &node{
			name:   base,
			parent: dirNode,
		}
		dirNode.rc += 1
		dirNode.children[base] = baseNode
	}
	return baseNode
}

// allocRetainExile allocates a node in the exile
// pseudo root and retain it.
func (tl *TreeLocker) allocRetainExile() *node {
	return &node{
		rc:     1, // retained
		name:   "",
		parent: nil,
		exile:  true,
	}
}

// free frees the node in the tree recursively.
func (n *node) free() {
	if n == nil {
		return
	}
	n.rc -= 1
	if n.rc == 0 && n.parent != nil {
		delete(n.parent.children, n.name)
		if len(n.parent.children) == 0 {
			childMapPool.Put(n.parent.children)
			n.parent.children = nil
		}
		n.parent.free()
	}
}

func (tl *TreeLocker) allocRetainClean(p string) *node {
	node := tl.allocClean(p)
	if node == nil {
		// Nothing need to be done to retain.
		return node
	}
	success := false
	defer func() {
		if !success {
			node.parent.free()
		}
	}()
	if node.rc == math.MaxUint64 {
		panic("too many references")
	}
	node.rc += 1
	success = true
	return node
}

// retain increments the reference counter.
func (n *node) retain() {
	// A nil node lives permanently, so there's
	// no need to increment its pointer.
	if n == nil {
		return
	}
	if n.rc == math.MaxUint64 {
		panic("too many references")
	}
	n.rc += 1
}

func cleanSlashPath(p string) string {
	return path.Clean(path.Join("/", p))
}

func cleanFilePath(p string) string {
	p = p[len(filepath.VolumeName(p)):]
	p = filepath.ToSlash(p)
	return cleanSlashPath(p)
}

// UnifyFilePath cleans and unifies a
// Windows file path.
//
// This is intended to be used by other module in a
// way that is uniform with the pathlock package.
func UnifyFilePath(p string) string {
	return filepath.FromSlash(cleanFilePath(p))
}

// nodeLocker is just a combination of a node and
// a locker, to provide appropriate operations.
// Its resource management is done by the object
// that wraps it.
type nodeLocker struct {
	locker *TreeLocker
	node   *node
}

// AddrAsID returns the address of underlying node
// as the unique identifier.
func (n *nodeLocker) AddrAsID() uint64 {
	return uint64(uintptr(unsafe.Pointer(n.node)))
}

func (n *node) isExile() bool {
	if n == nil {
		return false
	}
	if n.exile {
		return true
	}
	if n.parent.isExile() {
		// path compacting.
		n.exile = true
		return true
	}
	return false
}

// IsExile check if the node is under the
// exile pseudo root.
func (n *nodeLocker) IsExile() bool {
	n.locker.mtx.Lock()
	defer n.locker.mtx.Unlock()
	return n.node.isExile()
}

func (n *node) slashPathInner() string {
	if n == nil {
		return "/"
	}
	if n.name == "" {
		return "/"
	}
	return path.Join(n.parent.slashPathInner(), n.name)
}

func (n *node) slashPath() string {
	if n.isExile() {
		return ""
	}
	return n.slashPathInner()
}

// SlashPath atomically retrieves the current
// path of node in the tree.
//
// To keep the validity of path, you will need
// appropriate locking. A read or write path lock
// of this node should suffice.
func (n *nodeLocker) SlashPath() string {
	n.locker.mtx.Lock()
	defer n.locker.mtx.Unlock()
	return cleanSlashPath(n.node.slashPath())
}

func (n *nodeLocker) FilePath() string {
	return filepath.FromSlash(n.SlashPath())
}

// CurrentRefs returns the current reference
// counter of this node atomically.
//
// Please notice this does not prevent node
// from being retained from another thread.
// You will need proper locking and protocols
// between threads to ensure the validity of
// this reference counting.
func (n *nodeLocker) CurrentRefs() uint64 {
	n.locker.mtx.Lock()
	defer n.locker.mtx.Unlock()
	return n.node.rc
}

func (n *nodeLocker) HasChild() bool {
	n.locker.mtx.Lock()
	defer n.locker.mtx.Unlock()
	if n.node == nil {
		// nil node always has the root node
		// as children.
		return true
	}
	return len(n.node.children) > 0
}

type Node struct {
	nodeLocker
	once sync.Once
}

func (tl *TreeLocker) createNode(node *node) *Node {
	success := false
	node.retain()
	result := &Node{
		nodeLocker: nodeLocker{
			locker: tl,
			node:   node,
		},
	}
	defer func() {
		if !success {
			node.free()
		}
	}()
	runtime.SetFinalizer(result, func(l *Node) {
		l.Free()
	})
	success = true
	return result
}

func (tl *TreeLocker) allocCleanPath(p string) *Node {
	tl.mtx.Lock()
	defer tl.mtx.Unlock()
	node := tl.allocRetainClean(p)
	defer node.free()
	return tl.createNode(node)
}

func (tl *TreeLocker) AllocFile(p string) *Node {
	return tl.allocCleanPath(cleanFilePath(p))
}

func (tl *TreeLocker) AllocSlash(p string) *Node {
	return tl.allocCleanPath(cleanSlashPath(p))
}

func (tl *TreeLocker) AllocExile() *Node {
	tl.mtx.Lock()
	defer tl.mtx.Unlock()
	node := tl.allocRetainExile()
	defer node.free()
	return tl.createNode(node)
}

func (n *Node) Free() {
	runtime.SetFinalizer(n, nil)
	n.once.Do(func() {
		n.locker.mtx.Lock()
		defer n.locker.mtx.Unlock()
		n.node.free()
	})
}

// RetainNode obtains a new clone of the
// underlying node.
//
// The allocated node must be manually freed.
func (n *nodeLocker) RetainNode() *Node {
	n.locker.mtx.Lock()
	defer n.locker.mtx.Unlock()
	return n.locker.createNode(n.node)
}

// NodeLock is a lock for a single node.
//
// This lock does not prevent the tree path from
// being moved, however it serves as a node
// that can be attended to by other operations.
type NodeLock struct {
	nodeLocker
	write bool
	once  sync.Once
}

func (nl *NodeLock) IsWrite() bool {
	return nl.write
}

func (n *node) unlockNode(write bool) {
	if write {
		n.wunlockNode()
	} else {
		n.runlockNode()
	}
}

func (nl *NodeLock) unlockLocked() {
	defer nl.node.free()
	nl.node.unlockNode(nl.write)
}

func (nl *NodeLock) unlock() {
	nl.locker.mtx.Lock()
	defer nl.locker.mtx.Unlock()
	nl.unlockLocked()
}

func (nl *NodeLock) Unlock() {
	nl.once.Do(func() {
		nl.unlock()
	})
}

func (n *node) createNodeLock(locker *TreeLocker, write bool) *NodeLock {
	success := false
	defer func() {
		if !success {
			n.unlockNode(write)
		}
	}()
	result := &NodeLock{
		write: write,
		nodeLocker: nodeLocker{
			locker: locker,
			node:   n,
		},
	}
	result.node.retain()
	defer func() {
		if !success {
			result.node.free()
		}
	}()
	runtime.SetFinalizer(result, func(nl *NodeLock) {
		nl.Unlock()
	})
	success = true
	return result
}

func (n *Node) createNodeLock(write bool) *NodeLock {
	return n.node.createNodeLock(n.locker, write)
}

func (n *node) runlockNode() {
	if n == nil {
		return
	}
	if n.readers <= 0 {
		panic("invalid node state to read unlock")
	}
	n.readers -= 1
}

func (n *node) tryRlockNode(wait bool) (locked bool) {
	if n == nil {
		return true
	}
	if n.readers < 0 {
		if wait && n.waitCh == nil {
			n.waitCh = make(chan struct{})
		}
		return false
	}
	if n.readers == math.MaxInt64 {
		return false
	}
	n.readers += 1
	return true
}

func (n *Node) TryRLockNode() *NodeLock {
	n.locker.mtx.Lock()
	defer n.locker.mtx.Unlock()
	if n.node.tryRlockNode(false) {
		return n.createNodeLock(false)
	}
	return nil
}

func (n *Node) RLockNode() *NodeLock {
	for {
		result, waitCh := func() (*NodeLock, chan struct{}) {
			n.locker.mtx.Lock()
			defer n.locker.mtx.Unlock()
			if !n.node.tryRlockNode(true) {
				return nil, n.node.waitCh
			}
			return n.createNodeLock(false), nil
		}()
		if waitCh != nil {
			<-waitCh
			continue
		}
		return result
	}
}

func (n *node) wakeReaders() {
	if n.waitCh != nil {
		close(n.waitCh)
		n.waitCh = nil
	}
}

func (n *node) wunlockNode() {
	if n == nil {
		panic("nil node can never be write locked")
	}
	if n.readers != -1 {
		panic("invalid node state to write unlock")
	}
	n.readers = 0
	n.wakeReaders()
}

func (n *node) tryWLockNode() (locked bool) {
	if n == nil {
		return false
	}
	if n.readers != 0 {
		return false
	}
	n.readers = -1
	return true
}

func (n *Node) TryWLockNode() *NodeLock {
	n.locker.mtx.Lock()
	defer n.locker.mtx.Unlock()
	if n.node.tryWLockNode() {
		return n.createNodeLock(true)
	}
	return nil
}

func (n *NodeLock) TryUpgrade() bool {
	if n.write {
		panic("must only upgrade a read lock")
	}
	n.locker.mtx.Lock()
	defer n.locker.mtx.Unlock()
	if n.node.readers != 1 {
		return false
	}
	n.node.readers = -1
	n.write = true
	return true
}

func (n *NodeLock) Downgrade() {
	if !n.write {
		panic("must only downgrade a write lock")
	}
	n.locker.mtx.Lock()
	defer n.locker.mtx.Unlock()
	if n.node.readers != -1 {
		panic("invalid node state to downgrade")
	}
	n.node.readers = 1
	n.write = false
	n.node.wakeReaders()
}

// PathLock is a lock from the root to the node.
//
// Once this lock is acquired, no one can modify
// the nodes in this path anymore, so it is safe
// to acquire the tree path now.
type PathLock struct {
	nodeLocker
	write bool
	once  sync.Once
}

func (pl *PathLock) IsWrite() bool {
	return pl.write
}

func (n *node) unlockPath(write bool) {
	if n == nil {
		return
	}
	defer n.parent.runlockPath()
	if write {
		defer n.wunlockNode()
	} else {
		defer n.runlockNode()
	}
}

func (pl *PathLock) unlockLocked() {
	defer pl.node.free()
	pl.node.unlockPath(pl.write)
}

func (pl *PathLock) unlock() {
	pl.locker.mtx.Lock()
	defer pl.locker.mtx.Unlock()
	pl.unlockLocked()
}

func (pl *PathLock) Unlock() {
	runtime.SetFinalizer(pl, nil)
	pl.once.Do(func() {
		pl.unlock()
	})
}

func (node *node) createPathLock(locker *TreeLocker, write bool) *PathLock {
	success := false
	defer func() {
		if !success {
			node.unlockPath(write)
		}
	}()
	result := &PathLock{
		write: write,
		nodeLocker: nodeLocker{
			locker: locker,
			node:   node,
		},
	}
	result.node.retain()
	defer func() {
		if !success {
			result.node.free()
		}
	}()
	runtime.SetFinalizer(result, func(pl *PathLock) {
		pl.Unlock()
	})
	success = true
	return result
}

func (n *Node) createPathLock(write bool) *PathLock {
	return n.node.createPathLock(n.locker, write)
}

func (n *node) runlockPath() {
	if n == nil {
		return
	}
	defer n.parent.runlockPath()
	defer n.runlockNode()
}

func (n *node) tryRLockPath(wait bool) (blocker *node) {
	if n == nil {
		return nil
	}
	if blk := n.parent.tryRLockPath(wait); blk != nil {
		return blk
	}
	locked := false
	defer func() {
		if !locked {
			n.parent.runlockPath()
		}
	}()
	if !n.tryRlockNode(wait) {
		return n
	}
	locked = true
	return nil
}

func (n *Node) TryRLockPath() *PathLock {
	n.locker.mtx.Lock()
	defer n.locker.mtx.Unlock()
	if n.node.tryRLockPath(false) != nil {
		return nil
	}
	return n.createPathLock(false)
}

func (tl *TreeLocker) tryRLockClean(p string) *PathLock {
	tl.mtx.Lock()
	defer tl.mtx.Unlock()
	node := tl.allocRetainClean(p)
	defer node.free()
	if node.tryRLockPath(false) != nil {
		return nil
	}
	return node.createPathLock(tl, false)
}

// TryRLockSlash will try to acquire the reader
// lock of the slash path specified.
func (tl *TreeLocker) TryRLockSlash(p string) *PathLock {
	return tl.tryRLockClean(cleanSlashPath(p))
}

// TryRLockSlash will try to acquire the reader
// lock of the file path specified.
func (tl *TreeLocker) TryRLockFile(p string) *PathLock {
	return tl.tryRLockClean(cleanFilePath(p))
}

func (n *Node) RLockPath() *PathLock {
	for {
		result, waitCh := func() (*PathLock, chan struct{}) {
			n.locker.mtx.Lock()
			defer n.locker.mtx.Unlock()
			blocker := n.node.tryRLockPath(true)
			if blocker != nil {
				return nil, blocker.waitCh
			}
			return n.createPathLock(false), nil
		}()
		if waitCh != nil {
			<-waitCh
			continue
		}
		return result
	}
}

func (tl *TreeLocker) rlockClean(p string) *PathLock {
	for {
		result, waitCh := func() (*PathLock, chan struct{}) {
			tl.mtx.Lock()
			defer tl.mtx.Unlock()
			node := tl.allocRetainClean(p)
			defer node.free()
			blocker := node.tryRLockPath(true)
			if blocker != nil {
				return nil, blocker.waitCh
			}
			return node.createPathLock(tl, false), nil
		}()
		if waitCh != nil {
			<-waitCh
			continue
		}
		return result
	}
}

func (tl *TreeLocker) RLockSlash(p string) *PathLock {
	return tl.rlockClean(cleanSlashPath(p))
}

func (tl *TreeLocker) RLockFile(p string) *PathLock {
	return tl.rlockClean(cleanFilePath(p))
}

func (n *node) tryWLockPath() (acquired bool) {
	if n == nil {
		// Cannot acquire write lock of nil.
		return false
	}
	if n.readers != 0 {
		// Fails if there's reader or writer.
		return false
	}
	if n.parent.tryRLockPath(false) != nil {
		return false
	}
	locked := false
	defer func() {
		if !locked {
			n.parent.runlockPath()
		}
	}()
	locked = n.tryWLockNode()
	return locked
}

func (n *Node) TryWLockPath() *PathLock {
	n.locker.mtx.Lock()
	defer n.locker.mtx.Unlock()
	if n.node.tryWLockPath() {
		return n.createPathLock(true)
	}
	return nil
}

func (tl *TreeLocker) tryWLockClean(p string) *PathLock {
	tl.mtx.Lock()
	defer tl.mtx.Unlock()
	node := tl.allocRetainClean(p)
	defer node.free()
	if node.tryWLockPath() {
		return node.createPathLock(tl, true)
	}
	return nil
}

func (tl *TreeLocker) TryWLockSlash(p string) *PathLock {
	return tl.tryWLockClean(cleanSlashPath(p))
}

func (tl *TreeLocker) TryWLockFile(p string) *PathLock {
	return tl.tryWLockClean(cleanFilePath(p))
}

// Exchange the nodes locked by the two
// path locks in the tree.
//
// If p1 and p2 are not created from the
// same TreeLocker, or not write lock,
// then the function panics.
//
// After the exchanging, p1 holds the node
// that is held by p2, and vice versa.
func Exchange(p1, p2 *PathLock) {
	if p1.locker != p2.locker {
		panic("must be created from the same TreeLocker")
	}
	if !p1.write || !p2.write {
		// This also exclude the possibility of
		// p1.node == nil || p2.node == nil
		// Since we can't write-lock nil node.
		panic("must be write locks")
	}
	p1.locker.mtx.Lock()
	defer p1.locker.mtx.Unlock()
	p1Name, p2Name := p1.node.name, p2.node.name
	p1Parent, p2Parent := p1.node.parent, p2.node.parent
	if (p1Parent != nil && p1Parent.children[p1Name] != p1.node) ||
		(p2Parent != nil && p2Parent.children[p2Name] != p2.node) {
		panic("children mismatch")
	}
	p1Exile, p2Exile := p1.node.exile, p2.node.exile
	if (p1Exile && !p2Exile && len(p1.node.children) > 0) ||
		(p2Exile && !p1Exile && len(p2.node.children) > 0) {
		panic("cannot resurrent exiled tree")
	}
	if p1Parent != nil {
		p1Parent.children[p1Name] = p2.node
	}
	p2.node.name = p1Name
	p2.node.parent = p1Parent
	p2.node.exile = p1Exile
	if p2Parent != nil {
		p2Parent.children[p2Name] = p1.node
	}
	p1.node.parent = p2Parent
	p1.node.name = p2Name
	p1.node.exile = p2Exile
}

// Split the pathlock into a node lock plus the
// pathlock for its parent.
//
// The original pathlock will be consumed, since
// the path lock might be a write lock, and we
// can't hold two write locks simultaneously.
//
// The consumption is unconditional, even when
// the function panics.
func Split(pl *PathLock) (*PathLock, *NodeLock) {
	success := false

	plConsumed := true
	if pl != nil {
		pl.once.Do(func() {
			plConsumed = false
		})

		if !plConsumed {
			runtime.SetFinalizer(pl, nil)
			defer func() {
				if !success {
					pl.unlock()
				}
			}()
		}
	}

	if pl == nil {
		panic("cannot split nil pathlock")
	}
	if plConsumed {
		panic("pathlock already consumed")
	}

	pl.locker.mtx.Lock()
	defer pl.locker.mtx.Unlock()

	var parentNode *node
	if pl.node != nil {
		parentNode = pl.node.parent
	}

	parentLock := parentNode.createPathLock(pl.locker, false)
	defer func() {
		if !success {
			parentLock.unlockLocked()
		}
	}()
	nodeLock := pl.node.createNodeLock(pl.locker, pl.write)
	defer func() {
		if !success {
			nodeLock.unlockLocked()
		}
	}()
	pl.node.free()
	success = true
	return parentLock, nodeLock
}

// TryRLockParent will try to grow the parent
// path lock so that it's safe for operation.
func (nl *nodeLocker) TryRLockParent() *PathLock {
	nl.locker.mtx.Lock()
	defer nl.locker.mtx.Unlock()

	if nl.node.parent.tryRLockPath(false) == nil {
		return nl.node.parent.createPathLock(nl.locker, false)
	}
	return nil
}

// RLockParent is the blocking version of the
// TryRLockParent.
func (nl *nodeLocker) RLockParent() *PathLock {
	for {
		result, waitCh := func() (*PathLock, chan struct{}) {
			nl.locker.mtx.Lock()
			defer nl.locker.mtx.Unlock()
			blocker := nl.node.parent.tryRLockPath(true)
			if blocker != nil {
				return nil, blocker.waitCh
			}
			return nl.node.parent.createPathLock(nl.locker, false), nil
		}()
		if waitCh != nil {
			<-waitCh
			continue
		}
		return result
	}
}

// Join will join a node lock and its parent
// read path lock into a single path lock,
// consuming both of them.
//
// The consumption is unconditional, even when
// the function panics.
func Join(pl *PathLock, nl *NodeLock) *PathLock {
	plFreed := false
	plConsumed := true
	if pl != nil {
		pl.once.Do(func() {
			plConsumed = false
		})
		if !plConsumed {
			runtime.SetFinalizer(pl, nil)
			defer func() {
				if !plFreed {
					pl.unlock()
				}
			}()
		}
	}

	nlFreed := false
	nlConsumed := true
	if nl != nil {
		nl.once.Do(func() {
			nlConsumed = false
		})
		if !nlConsumed {
			runtime.SetFinalizer(nl, nil)
			defer func() {
				if !nlFreed {
					nl.unlock()
				}
			}()
		}
	}

	if pl == nil {
		panic("parent pathlock cannot be nil")
	}
	if nl == nil {
		panic("nodelock cannot be nil")
	}
	if plConsumed {
		panic("parent pathlock already consumed")
	}
	if nlConsumed {
		panic("nodelock already consumed")
	}
	if pl.locker != nl.locker {
		panic("must be created from the same TreeLocker")
	}
	if pl.write {
		panic("parent must only be read lock")
	}

	pl.locker.mtx.Lock()
	defer pl.locker.mtx.Unlock()
	if (nl.node == nil && pl.node != nil) || (nl.node.parent != pl.node) {
		panic("parent pathlock is not parent of the nodelock")
	}
	joinedLock := nl.node.createPathLock(nl.locker, nl.write)
	success := false
	defer func() {
		if !success {
			joinedLock.unlockLocked()
		}
	}()
	nl.node.free()
	nlFreed = true
	pl.node.free()
	plFreed = true
	success = true
	return joinedLock
}

func (tl *TreeLocker) WLockExile() *PathLock {
	tl.mtx.Lock()
	defer tl.mtx.Unlock()
	exile := tl.allocRetainExile()
	defer exile.free()
	if !exile.tryWLockPath() {
		panic("write lock exile failed")
	}
	return exile.createPathLock(tl, true)
}
