package treelock

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type Assert struct {
	*assert.Assertions
}

func (assert *Assert) EmptyLocker(tl *TreeLocker) {
	tl.mtx.Lock()
	defer tl.mtx.Unlock()
	assert.Equal(uint64(0), tl.root.rc)
	assert.Equal(int64(0), tl.root.readers)
	assert.Equal(0, len(tl.root.children))
}

func TestResolution(t *testing.T) {
	assert := Assert{assert.New(t)}
	tl := New()
	assert.EmptyLocker(tl)
	defer assert.EmptyLocker(tl)

	nodeRoot := tl.AllocSlash("/")
	defer nodeRoot.Free()
	assert.Equal(&tl.root, &nodeRoot.node)
	assert.Equal("/", nodeRoot.SlashPath())
	assert.Equal(filepath.FromSlash("/"), nodeRoot.FilePath())
	func() {
		node1 := tl.AllocFile("")
		defer node1.Free()
		assert.Equal(&nodeRoot.node, &node1.node)

		node2 := tl.AllocFile(".")
		defer node2.Free()
		assert.Equal(&nodeRoot.node, &node2.node)

		node3 := tl.AllocSlash("")
		defer node3.Free()
		assert.Equal(&nodeRoot.node, &node3.node)

		node4 := tl.AllocSlash(".")
		defer node4.Free()
		assert.Equal(&nodeRoot.node, &node4.node)

		node5 := tl.AllocSlash("./")
		defer node5.Free()
		assert.Equal(&nodeRoot.node, &node5.node)

		// The "Root Cage"
		node6 := tl.AllocSlash("..")
		defer node6.Free()
		assert.Equal(&nodeRoot.node, &node6.node)

		node7 := tl.AllocSlash("../")
		defer node7.Free()
		assert.Equal(&nodeRoot.node, &node7.node)

		node8 := tl.AllocSlash("../..")
		defer node8.Free()
		assert.Equal(&nodeRoot.node, &node8.node)

		node9 := tl.AllocSlash("//")
		defer node9.Free()
		assert.Equal(&nodeRoot.node, &node9.node)
	}()

	nodeA := tl.AllocFile("/a")
	defer nodeA.Free()
	childNodeA := nodeRoot.node.children["a"]
	assert.NotEqual(&childNodeA, nodeRoot.node)
	assert.Equal(&childNodeA, &nodeA.node)
	assert.Equal("/a", nodeA.SlashPath())
	assert.Equal(filepath.FromSlash("/a"), nodeA.FilePath())
	func() {
		node1 := tl.AllocFile(filepath.FromSlash("/a"))
		defer node1.Free()
		assert.Equal(&nodeA.node, &node1.node)

		node2 := tl.AllocSlash("a")
		defer node2.Free()
		assert.Equal(&nodeA.node, &node2.node)

		node3 := tl.AllocSlash("a/")
		defer node3.Free()
		assert.Equal(&nodeA.node, &node3.node)

		node4 := tl.AllocSlash("../a")
		defer node4.Free()
		assert.Equal(&nodeA.node, &node4.node)

		node5 := tl.AllocSlash("/b/../a")
		defer node5.Free()
		assert.Equal(&nodeA.node, &node5.node)
	}()

	nodeB := tl.AllocFile("b")
	defer nodeB.Free()
	childNodeB := nodeRoot.node.children["b"]
	assert.NotEqual(&childNodeB, nodeRoot.node)
	assert.NotEqual(&childNodeA, &childNodeB)
	assert.Equal(&childNodeB, &nodeB.node)
	assert.Equal("/b", nodeB.SlashPath())
	assert.Equal(filepath.FromSlash("/b"), nodeB.FilePath())
	func() {
		node1 := tl.AllocFile(filepath.FromSlash("/b"))
		defer node1.Free()
		assert.Equal(&nodeB.node, &node1.node)

		node2 := tl.AllocSlash("/a/../b")
		defer node2.Free()
		assert.Equal(&nodeB.node, &node2.node)
	}()
}

func TestOperation(t *testing.T) {
	assert := Assert{assert.New(t)}
	tl := New()
	assert.EmptyLocker(tl)
	defer assert.EmptyLocker(tl)

	nodeAB1 := tl.AllocSlash("/a/b")
	assert.NotNil(nodeAB1)
	defer nodeAB1.Free()

	assert.Equal(uint64(1), tl.root.rc)
	assert.Equal(uint64(1), tl.root.children["a"].rc)
	assert.Equal(uint64(1), tl.root.children["a"].children["b"].rc)
	assert.Equal(uint64(1), nodeAB1.CurrentRefs())
	assert.Equal(false, nodeAB1.HasChild())

	nodeAB2 := nodeAB1.RetainNode()
	assert.NotNil(nodeAB2)
	defer nodeAB2.Free()

	assert.Equal(uint64(1), tl.root.rc)
	assert.Equal(uint64(1), tl.root.children["a"].rc)
	assert.Equal(uint64(2), tl.root.children["a"].children["b"].rc)
	assert.Equal(&nodeAB1.node, &nodeAB2.node)
	assert.Equal(uint64(2), nodeAB1.CurrentRefs())
	assert.Equal(uint64(2), nodeAB2.CurrentRefs())
	assert.Equal(false, nodeAB1.HasChild())
	assert.Equal(false, nodeAB2.HasChild())

	nodeAB3 := tl.AllocSlash("/a/b")
	assert.NotNil(nodeAB3)
	defer nodeAB3.Free()

	assert.Equal(uint64(1), tl.root.rc)
	assert.Equal(uint64(1), tl.root.children["a"].rc)
	assert.Equal(uint64(3), tl.root.children["a"].children["b"].rc)
	assert.Equal(&nodeAB1.node, &nodeAB3.node)
	assert.Equal(uint64(3), nodeAB1.CurrentRefs())
	assert.Equal(uint64(3), nodeAB2.CurrentRefs())
	assert.Equal(uint64(3), nodeAB3.CurrentRefs())
	assert.Equal(false, nodeAB1.HasChild())
	assert.Equal(false, nodeAB2.HasChild())
	assert.Equal(false, nodeAB3.HasChild())

	nodeA1 := tl.AllocSlash("/a")
	assert.NotNil(nodeA1)
	defer nodeA1.Free()

	assert.Equal(uint64(1), tl.root.rc)
	assert.Equal(uint64(2), tl.root.children["a"].rc)
	assert.Equal(uint64(3), tl.root.children["a"].children["b"].rc)
	assert.Equal(&nodeA1.node, &nodeAB1.node.parent)
	assert.Equal(uint64(2), nodeA1.CurrentRefs())
	assert.Equal(true, nodeA1.HasChild())
}

func TestPathRLockShare(t *testing.T) {
	assert := Assert{assert.New(t)}
	tl := New()
	assert.EmptyLocker(tl)
	defer assert.EmptyLocker(tl)

	rlockRoot := tl.TryRLockSlash("/")
	assert.NotNil(rlockRoot)
	defer rlockRoot.Unlock()
	// Locking path like "/a" will require
	// read-locking "/", which will be permitted.

	rlock1 := tl.TryRLockSlash("a")
	assert.NotNil(rlock1)
	defer rlock1.Unlock()

	rlock2 := tl.TryRLockSlash("a")
	assert.NotNil(rlock2)
	defer rlock2.Unlock()

	node1 := tl.AllocSlash("a")
	assert.NotNil(node1)
	defer node1.Free()

	rlock3 := node1.TryRLockPath()
	assert.NotNil(rlock3)
	defer rlock3.Unlock()

	rlock4 := node1.TryRLockNode()
	assert.NotNil(rlock4)
	defer rlock4.Unlock()

	// rlockRoot, rlock1, rlock2, rlock3
	assert.Equal(int64(4), tl.root.readers)

	// rlock1, rlock2, rlock3, rlock4
	assert.Equal(int64(4), tl.root.children["a"].readers)
}

func TestNodeRLockShare(t *testing.T) {
	assert := Assert{assert.New(t)}
	tl := New()
	assert.EmptyLocker(tl)
	defer assert.EmptyLocker(tl)

	node1 := tl.AllocSlash("/a")
	assert.NotNil(node1)
	defer node1.Free()

	rlock1 := node1.TryRLockNode()
	assert.NotNil(rlock1)
	defer rlock1.Unlock()

	rlock2 := node1.TryRLockNode()
	assert.NotNil(rlock2)
	defer rlock2.Unlock()

	node2 := tl.AllocSlash("/a")
	assert.NotNil(node2)
	defer node2.Free()

	rlock3 := node2.TryRLockNode()
	assert.NotNil(rlock3)
	defer rlock3.Unlock()

	rlock4 := tl.TryRLockSlash("/a")
	assert.NotNil(rlock4)
	defer rlock4.Unlock()

	// rlock4
	assert.Equal(int64(1), tl.root.readers)

	// rlock1, rlock2, rlock3, rlock4
	assert.Equal(int64(4), tl.root.children["a"].readers)
}

func TestPathWLockExclusive(t *testing.T) {
	assert := Assert{assert.New(t)}
	tl := New()
	assert.EmptyLocker(tl)
	defer assert.EmptyLocker(tl)

	func() {
		rlockRoot := tl.TryRLockSlash("/")
		assert.NotNil(rlockRoot)
		defer rlockRoot.Unlock()

		// Read-locking root path will
		// prevent the write-locking later.
		assert.Nil(tl.TryWLockSlash("/"))

		// However it will not forbid locking
		// path under the root.
		wlockA1 := tl.TryWLockSlash("/a")
		assert.NotNil(wlockA1)
		defer wlockA1.Unlock()

		// Then this write lock will prevent others
		// from read-locking or write-locking "/a".
		assert.Nil(tl.TryRLockSlash("/a"))
		assert.Nil(tl.TryWLockSlash("/a"))

		nodeA := tl.AllocSlash("/a")
		defer nodeA.Free()
		assert.Nil(nodeA.TryRLockNode())
		assert.Nil(nodeA.TryWLockNode())

		// rlockRoot, wlockA1
		assert.Equal(int64(2), tl.root.readers)

		// wlockA1
		assert.Equal(int64(-1), tl.root.children["a"].readers)
	}()

	func() {
		// Conversely, locking different path without
		// forming cycles is permitted.
		wlockA := tl.TryWLockSlash("/a")
		assert.NotNil(wlockA)
		defer wlockA.Unlock()

		wlockB := tl.TryWLockSlash("/b")
		assert.NotNil(wlockB)
		defer wlockB.Unlock()

		rlockRoot := tl.TryRLockSlash("/")
		assert.NotNil(rlockRoot)
		defer rlockRoot.Unlock()

		rlockC := tl.TryRLockSlash("/c")
		assert.NotNil(rlockC)
		defer rlockC.Unlock()

		// wlockA, wlockB, rlockRoot, rlockC
		assert.Equal(int64(4), tl.root.readers)

		// wlockA
		assert.Equal(int64(-1), tl.root.children["a"].readers)

		// wlockB
		assert.Equal(int64(-1), tl.root.children["b"].readers)

		// rlockC
		assert.Equal(int64(1), tl.root.children["c"].readers)
	}()

	func() {
		// Some cycle prevention cases test.
		wlockPA := tl.TryWLockSlash("/p/a")
		assert.NotNil(wlockPA)
		defer wlockPA.Unlock()

		func() {
			rlockP := tl.TryRLockSlash("/p")
			assert.NotNil(rlockP)
			defer rlockP.Unlock()
		}()

		assert.Nil(tl.TryWLockSlash("/p"))
		assert.Nil(tl.TryRLockSlash("/p/a/b"))
		assert.Nil(tl.TryWLockSlash("/p/a/b"))

		// wlockPA
		assert.Equal(int64(1), tl.root.readers)

		// wlockPA
		assert.Equal(int64(1), tl.root.children["p"].readers)

		// wlockPA
		assert.Equal(int64(-1), tl.root.children["p"].children["a"].readers)
	}()
}

func TestNodeWLockExclusive(t *testing.T) {
	assert := Assert{assert.New(t)}
	tl := New()
	assert.EmptyLocker(tl)
	defer assert.EmptyLocker(tl)

	func() {
		node1 := tl.AllocSlash("/")
		assert.NotNil(node1)
		defer node1.Free()

		rlock1 := node1.TryRLockNode()
		assert.NotNil(rlock1)
		defer rlock1.Unlock()

		assert.Nil(node1.TryWLockNode())

		node2 := tl.AllocSlash("/")
		assert.NotNil(node2)
		defer node2.Free()

		assert.Nil(node2.TryWLockNode())

		// rlock1
		assert.Equal(int64(1), tl.root.readers)
	}()

	func() {
		node1 := tl.AllocSlash("/")
		assert.NotNil(node1)
		defer node1.Free()

		wlock1 := node1.TryWLockNode()
		assert.NotNil(wlock1)
		defer wlock1.Unlock()

		assert.Nil(node1.TryRLockNode())

		// Since the root node is locked, and
		// path lock requires locking from root
		// to the node, they will be rejected.
		node2 := tl.AllocSlash("/a")
		defer node2.Free()

		assert.Nil(node2.TryRLockPath())
		assert.Nil(node2.TryWLockPath())
		assert.Nil(node2.TryRLockParent())

		node3 := tl.AllocSlash("/")
		assert.NotNil(node3)
		defer node3.Free()

		assert.Nil(node3.TryRLockNode())
		assert.Nil(node3.TryWLockNode())

		// wlock1
		assert.Equal(int64(-1), tl.root.readers)
	}()
}

func TestPathReadAfterWriteLock(t *testing.T) {
	assert := Assert{assert.New(t)}
	tl := New()
	assert.EmptyLocker(tl)
	defer assert.EmptyLocker(tl)

	var wg sync.WaitGroup
	defer wg.Wait()

	wlockA := tl.TryWLockSlash("/a")
	assert.NotNil(wlockA)
	defer wlockA.Unlock()

	assert.Nil(tl.TryRLockSlash("/a"))

	numReaders := 10
	for i := 0; i < numReaders; i++ {
		wg.Go(func() {
			rlockA := tl.RLockSlash("/a")
			assert.NotNil(rlockA)
			defer rlockA.Unlock()
		})

		wg.Go(func() {
			nodeA := tl.AllocSlash("/a")
			assert.NotNil(nodeA)
			defer nodeA.Free()

			rlockA := nodeA.RLockNode()
			assert.NotNil(rlockA)
			defer rlockA.Unlock()
		})

		wg.Go(func() {
			nodeA := tl.AllocSlash("/a")
			assert.NotNil(nodeA)
			defer nodeA.Free()

			rlockA := nodeA.RLockPath()
			assert.NotNil(rlockA)
			defer rlockA.Unlock()
		})

		wg.Go(func() {
			nodeAB := tl.AllocSlash("/a/b")
			assert.NotNil(nodeAB)
			defer nodeAB.Free()

			rlockA := nodeAB.RLockParent()
			assert.NotNil(rlockA)
			defer rlockA.Unlock()
		})
	}

	// A tiny timeout to let the reader
	// goroutines run.
	<-time.After(10 * time.Millisecond)
}

func TestPathIsWrite(t *testing.T) {
	assert := Assert{assert.New(t)}
	tl := New()
	assert.EmptyLocker(tl)
	defer assert.EmptyLocker(tl)

	func() {
		rlock := tl.TryRLockSlash("/a")
		assert.NotNil(rlock)
		defer rlock.Unlock()
		assert.Equal(false, rlock.IsWrite())
	}()

	func() {
		wlock := tl.TryWLockSlash("/a")
		assert.NotNil(wlock)
		defer wlock.Unlock()
		assert.Equal(true, wlock.IsWrite())
	}()
}

func TestNodeIsWrite(t *testing.T) {
	assert := Assert{assert.New(t)}
	tl := New()
	assert.EmptyLocker(tl)
	defer assert.EmptyLocker(tl)

	node := tl.AllocSlash("/a")
	defer node.Free()

	func() {
		rlock := node.TryRLockNode()
		assert.NotNil(rlock)
		defer rlock.Unlock()
		assert.Equal(false, rlock.IsWrite())
	}()

	func() {
		wlock := node.TryWLockNode()
		assert.NotNil(wlock)
		defer wlock.Unlock()
		assert.Equal(true, wlock.IsWrite())
	}()
}

func TestExchange(t *testing.T) {
	assert := Assert{assert.New(t)}
	tl := New()
	assert.EmptyLocker(tl)
	defer assert.EmptyLocker(tl)

	func() {
		wlock1 := tl.TryWLockSlash("/a/b")
		assert.NotNil(wlock1)
		defer wlock1.Unlock()

		wlock2 := tl.TryWLockSlash("/c/d")
		assert.NotNil(wlock2)
		defer wlock2.Unlock()

		nodeA := tl.root.children["a"]
		nodeC := tl.root.children["c"]

		assert.Equal(int64(2), tl.root.readers)

		assert.Equal(int64(1), nodeA.readers)
		childNodeB1 := nodeA.children["b"]
		assert.Equal(&wlock1.node, &childNodeB1)
		assert.Equal(int64(-1), wlock1.node.readers)
		assert.Equal(&nodeA, &wlock1.node.parent)
		assert.Equal(wlock1.node.exile, false)

		assert.Equal(int64(1), nodeC.readers)
		childNodeD1 := nodeC.children["d"]
		assert.Equal(&wlock2.node, &childNodeD1)
		assert.Equal(int64(-1), wlock2.node.readers)
		assert.Equal(&nodeC, &wlock2.node.parent)
		assert.Equal(wlock2.node.exile, false)

		func() {
			nodeAB := tl.AllocSlash("/a/b")
			assert.NotNil(nodeAB)
			defer nodeAB.Free()
			assert.Equal(&wlock1.node, &nodeAB.node)

			nodeCD := tl.AllocSlash("/c/d")
			assert.NotNil(nodeCD)
			defer nodeCD.Free()
			assert.Equal(&wlock2.node, &nodeCD.node)
		}()

		Exchange(wlock1, wlock2)

		assert.Equal(int64(2), tl.root.readers)

		assert.Equal(int64(1), nodeA.readers)
		childNodeB2 := nodeA.children["b"]
		assert.Equal(&wlock2.node, &childNodeB2)
		assert.Equal(int64(-1), wlock2.node.readers)
		assert.Equal(&nodeA, &wlock2.node.parent)
		assert.Equal(wlock2.node.exile, false)

		assert.Equal(int64(-1), nodeA.children["b"].readers)
		childNodeD2 := nodeC.children["d"]
		assert.Equal(&wlock1.node, &childNodeD2)
		assert.Equal(int64(-1), wlock1.node.readers)
		assert.Equal(&nodeC, &wlock1.node.parent)
		assert.Equal(wlock1.node.exile, false)

		func() {
			nodeAB := tl.AllocSlash("/a/b")
			assert.NotNil(nodeAB)
			defer nodeAB.Free()
			assert.Equal(&wlock2.node, &nodeAB.node)

			nodeCD := tl.AllocSlash("/c/d")
			assert.NotNil(nodeCD)
			defer nodeCD.Free()
			assert.Equal(&wlock1.node, &nodeCD.node)
		}()
	}()

	func() {
		wlock1 := tl.TryWLockSlash("/a")
		assert.NotNil(wlock1)
		defer wlock1.Unlock()

		wlock2 := tl.TryWLockSlash("/b")
		assert.NotNil(wlock2)
		defer wlock2.Unlock()

		// wlock1, wlock2
		assert.Equal(int64(2), tl.root.readers)

		childNodeA1 := tl.root.children["a"]
		assert.Equal(&wlock1.node, &childNodeA1)
		assert.Equal(int64(-1), wlock1.node.readers)
		assert.Equal(&tl.root, &wlock1.node.parent)
		assert.Equal(wlock1.node.exile, false)

		childNodeB1 := tl.root.children["b"]
		assert.Equal(&wlock2.node, &childNodeB1)
		assert.Equal(int64(-1), wlock2.node.readers)
		assert.Equal(&tl.root, &wlock2.node.parent)
		assert.Equal(wlock2.node.exile, false)

		func() {
			nodeA := tl.AllocSlash("/a")
			assert.NotNil(nodeA)
			defer nodeA.Free()
			assert.Equal(&wlock1.node, &nodeA.node)

			nodeB := tl.AllocSlash("/b")
			assert.NotNil(nodeB)
			defer nodeB.Free()
			assert.Equal(&wlock2.node, &nodeB.node)
		}()

		Exchange(wlock1, wlock2)

		assert.Equal(int64(2), tl.root.readers)

		childNodeA2 := tl.root.children["a"]
		assert.Equal(&wlock2.node, &childNodeA2)
		assert.Equal(int64(-1), wlock2.node.readers)
		assert.Equal(&tl.root, &wlock2.node.parent)
		assert.Equal(wlock2.node.exile, false)

		childNodeB2 := tl.root.children["b"]
		assert.Equal(&wlock1.node, &childNodeB2)
		assert.Equal(int64(-1), wlock1.node.readers)
		assert.Equal(&tl.root, &wlock1.node.parent)
		assert.Equal(wlock1.node.exile, false)

		func() {
			nodeA := tl.AllocSlash("/a")
			assert.NotNil(nodeA)
			defer nodeA.Free()
			assert.Equal(&wlock2.node, &nodeA.node)

			nodeB := tl.AllocSlash("/b")
			assert.NotNil(nodeB)
			defer nodeB.Free()
			assert.Equal(&wlock1.node, &nodeB.node)
		}()
	}()

	func() {
		wlock1 := tl.TryWLockSlash("/a")
		assert.NotNil(wlock1)
		defer wlock1.Unlock()

		wlock2 := tl.WLockExile()
		assert.NotNil(wlock2)
		defer wlock2.Unlock()

		// wlock1
		assert.Equal(int64(1), tl.root.readers)

		childNodeA1 := tl.root.children["a"]
		assert.Equal(&wlock1.node, &childNodeA1)
		assert.Equal(int64(-1), wlock1.node.readers)
		assert.Equal(&tl.root, &wlock1.node.parent)
		assert.Equal(wlock1.node.exile, false)

		assert.Equal(int64(-1), wlock2.node.readers)
		assert.Nil(wlock2.node.parent)
		assert.Equal(wlock2.node.exile, true)

		func() {
			nodeA := tl.AllocSlash("/a")
			assert.NotNil(nodeA)
			defer nodeA.Free()
			assert.Equal(&wlock1.node, &nodeA.node)
		}()

		Exchange(wlock1, wlock2)

		// wlock2
		assert.Equal(int64(1), tl.root.readers)

		childNodeA2 := tl.root.children["a"]
		assert.Equal(&wlock2.node, &childNodeA2)
		assert.Equal(int64(-1), wlock2.node.readers)
		assert.Equal(&tl.root, &wlock2.node.parent)
		assert.Equal(wlock2.node.exile, false)

		assert.Equal(int64(-1), wlock1.node.readers)
		assert.Nil(wlock1.node.parent)
		assert.Equal(wlock1.node.exile, true)

		func() {
			nodeA := tl.AllocSlash("/a")
			assert.NotNil(nodeA)
			defer nodeA.Free()
			assert.Equal(&wlock2.node, &nodeA.node)
		}()
	}()

	func() {
		wlock1 := tl.WLockExile()
		assert.NotNil(wlock1)
		defer wlock1.Unlock()

		wlock2 := tl.WLockExile()
		assert.NotNil(wlock2)
		defer wlock2.Unlock()

		assert.Equal(int64(0), tl.root.readers)

		assert.Equal(int64(-1), wlock1.node.readers)
		assert.Nil(wlock1.node.parent)
		assert.Equal(wlock1.node.exile, true)

		assert.Equal(int64(-1), wlock2.node.readers)
		assert.Nil(wlock2.node.parent)
		assert.Equal(wlock2.node.exile, true)

		Exchange(wlock1, wlock2)

		assert.Equal(int64(0), tl.root.readers)

		assert.Equal(int64(-1), wlock1.node.readers)
		assert.Nil(wlock1.node.parent)
		assert.Equal(wlock1.node.exile, true)

		assert.Equal(int64(-1), wlock2.node.readers)
		assert.Nil(wlock2.node.parent)
		assert.Equal(wlock2.node.exile, true)
	}()
}

func TestExileTree(t *testing.T) {
	assert := Assert{assert.New(t)}
	tl := New()
	assert.EmptyLocker(tl)
	defer assert.EmptyLocker(tl)

	node1 := tl.AllocSlash("/a/b")
	defer node1.Free()
	assert.Equal(node1.IsExile(), false)

	wlock1 := tl.TryWLockSlash("/a")
	assert.NotNil(wlock1)
	defer wlock1.Unlock()
	assert.Equal(wlock1.IsExile(), false)

	node2 := tl.AllocExile()
	assert.NotNil(node2)
	defer node2.Free()
	assert.Equal(node2.IsExile(), true)

	wlock2 := node2.TryWLockPath()
	assert.NotNil(wlock2)
	defer wlock2.Unlock()
	assert.Equal(node2.IsExile(), true)

	Exchange(wlock1, wlock2)

	assert.Equal(node1.IsExile(), true)
	assert.Equal(wlock1.IsExile(), true)
	assert.Equal(node2.IsExile(), false)
}

func TestSplit(t *testing.T) {
	assert := Assert{assert.New(t)}
	tl := New()
	assert.EmptyLocker(tl)
	defer assert.EmptyLocker(tl)

	func() {
		wlock1 := tl.TryWLockSlash("/a/b")
		assert.NotNil(wlock1)
		defer func() {
			if wlock1 != nil {
				wlock1.Unlock()
			}
		}()

		// wlock1
		assert.Equal(int64(1), tl.root.readers)
		assert.Equal(int64(1), tl.root.children["a"].readers)
		assert.Equal(int64(-1), tl.root.children["a"].children["b"].readers)

		plock1, nlock1 := Split(wlock1)
		wlock1 = nil
		defer func() {
			if plock1 != nil {
				plock1.Unlock()
			}
		}()
		defer func() {
			if nlock1 != nil {
				nlock1.Unlock()
			}
		}()

		// plock1
		assert.Equal(int64(1), tl.root.readers)
		assert.Equal(int64(1), tl.root.children["a"].readers)
		// nlock1
		assert.Equal(int64(-1), tl.root.children["a"].children["b"].readers)

		plock1.Unlock()
		plock1 = nil

		assert.Equal(int64(0), tl.root.readers)
		assert.Equal(int64(0), tl.root.children["a"].readers)
		// nlock1
		assert.Equal(int64(-1), tl.root.children["a"].children["b"].readers)

		nlock1.Unlock()
		nlock1 = nil

		assert.Equal(int64(0), tl.root.readers)
		assert.Equal(int(0), len(tl.root.children))
	}()

	func() {
		var nilNode *node

		plock1 := tl.TryWLockSlash("/")
		assert.NotNil(plock1)
		defer func() {
			if plock1 != nil {
				plock1.Unlock()
			}
		}()
		assert.Equal(&tl.root, &plock1.node)

		plock2, nlock2 := Split(plock1)
		plock1 = nil
		defer func() {
			if plock2 != nil {
				plock2.Unlock()
			}
		}()
		defer func() {
			if nlock2 != nil {
				nlock2.Unlock()
			}
		}()
		assert.NotNil(plock2)
		assert.NotNil(nlock2)

		assert.Equal(&nilNode, &plock2.node)
		assert.Equal(&tl.root, &nlock2.node)

		plock3, nlock3 := Split(plock2)
		plock2 = nil
		defer func() {
			if plock3 != nil {
				plock3.Unlock()
			}
		}()
		defer func() {
			if nlock3 != nil {
				nlock3.Unlock()
			}
		}()
		assert.NotNil(plock3)
		assert.NotNil(nlock3)

		assert.Equal(&nilNode, &plock3.node)
		assert.Equal(&nilNode, &nlock3.node)
	}()
}

func TestJoin(t *testing.T) {
	assert := Assert{assert.New(t)}
	tl := New()
	assert.EmptyLocker(tl)
	defer assert.EmptyLocker(tl)

	func() {
		node1 := tl.AllocSlash("/a/b")
		assert.NotNil(node1)
		defer node1.Free()

		nlock1 := node1.TryWLockNode()
		assert.NotNil(nlock1)
		defer func() {
			if nlock1 != nil {
				nlock1.Unlock()
			}
		}()

		assert.Equal(int64(0), tl.root.readers)
		assert.Equal(int64(0), tl.root.children["a"].readers)
		// nlock1
		assert.Equal(int64(-1), tl.root.children["a"].children["b"].readers)

		plock1 := node1.TryRLockParent()
		assert.NotNil(plock1)

		// plock1
		assert.Equal(int64(1), tl.root.readers)
		assert.Equal(int64(1), tl.root.children["a"].readers)
		// nlock1
		assert.Equal(int64(-1), tl.root.children["a"].children["b"].readers)

		wlock1 := Join(plock1, nlock1)
		plock1, nlock1 = nil, nil
		defer func() {
			if wlock1 != nil {
				wlock1.Unlock()
			}
		}()

		// wlock1
		assert.Equal(int64(1), tl.root.readers)
		assert.Equal(int64(1), tl.root.children["a"].readers)
		assert.Equal(int64(-1), tl.root.children["a"].children["b"].readers)

		wlock1.Unlock()
		wlock1 = nil

		assert.Equal(int64(0), tl.root.readers)
		// node1 is active
		assert.Equal(int64(0), tl.root.children["a"].readers)
		assert.Equal(int64(0), tl.root.children["a"].children["b"].readers)
	}()
}

func TestUpgrade(t *testing.T) {
	assert := Assert{assert.New(t)}
	tl := New()
	assert.EmptyLocker(tl)
	defer assert.EmptyLocker(tl)

	node := tl.AllocSlash("/a")
	assert.NotNil(node)
	defer node.Free()

	func() {
		lock1 := node.TryRLockNode()
		assert.NotNil(lock1)
		defer lock1.Unlock()
		assert.Equal(false, lock1.IsWrite())

		success := lock1.TryUpgrade()
		assert.Equal(true, success)

		assert.Equal(true, lock1.IsWrite())
	}()

	func() {
		lock1 := node.TryRLockNode()
		assert.NotNil(lock1)
		defer lock1.Unlock()
		assert.Equal(lock1.IsWrite(), false)

		lock2 := node.TryRLockNode()
		assert.NotNil(lock2)
		defer lock2.Unlock()
		assert.Equal(lock2.IsWrite(), false)

		success := lock1.TryUpgrade()
		assert.Equal(false, success)

		assert.Equal(false, lock1.IsWrite())
	}()
}

func TestDowngrade(t *testing.T) {
	assert := Assert{assert.New(t)}
	tl := New()
	assert.EmptyLocker(tl)
	defer assert.EmptyLocker(tl)

	node := tl.AllocSlash("/a")
	assert.NotNil(node)
	defer node.Free()

	func() {
		lock1 := node.TryWLockNode()
		assert.NotNil(lock1)
		defer lock1.Unlock()
		assert.Equal(true, lock1.IsWrite())
		lock1.Downgrade()
		assert.Equal(false, lock1.IsWrite())
	}()
}
