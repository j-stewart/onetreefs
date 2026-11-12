package onetreefs

import (
	"log"
	"slices"
	"sync"
	"syscall"
)

type Snapshot struct {
	name      string
	gen       uint32
	sr        *SuperRoot
	root      BlockRef
	lastErrno syscall.Errno
	refcounts map[BlockRef]int
	deaths    map[BlockRef][]BlockRef
	lock      sync.Mutex
}

type SnapshotMutator struct {
	*Superblock
	gen     uint32
	parent  Mutator
	births  map[uint64]bool   // blocks added this upsert
	zombies map[uint64]bool   // blocks added then obsoleted this upsert
	pending map[uint64][]byte // pending writes
	deaths  []BlockRef        // blocks deleted this upsert
}

// Note: Load() will never find the pending blocks in pending; we rely on them being in cache

func (snap *Snapshot) Superblock() *Superblock {
	return &snap.sr.Superblock
}

func (snap *Snapshot) FreeBlocks() uint64 {
	return snap.sr.log.totalFree
}

func (snap *Snapshot) NewSnapshotMutator() *SnapshotMutator {
	return &SnapshotMutator{
		&snap.sr.Superblock,
		snap.gen,
		snap.sr,
		make(map[uint64]bool),
		make(map[uint64]bool),
		make(map[uint64][]byte),
		nil,
	}
}

func (snap *Snapshot) Upsert(kvs []KV) {
	snap.lock.Lock()
	slices.SortFunc(kvs, func(a, b KV) int { return CmpKeys(a.Key(), b.Key()) })

	mut := snap.NewSnapshotMutator()
	var newroot BlockRef
	rootNode := snap.sr.Load(snap.root)
	if errNode, isErr := rootNode.(*ErrorNode); isErr {
		snap.lastErrno = errNode.errno
		snap.lock.Unlock()
		log.Printf("error loading root: %s\n", errNode.errno.Error())
		return
	}
	newroots := rootNode.Upsert(kvs, snap.root, mut)
	// work out if we need a new root
	if len(newroots) == 1 {
		newroot = newroots[0].block
	} else if len(newroots) == 0 {
		// the newroot should be an empty leaf
		newroot = mut.Enqueue(&Leaf{nil}).block
	} else {
		// the newroot should be a branch with newroots as children
		newroot = mut.Enqueue(&Branch{snap.sr, newroots, nil}).block
	}

	log.Printf("Root is now %v\n", newroot)

	snap.root = newroot
	snap.deaths[snap.root] = mut.deaths
	snap.lock.Unlock()

	errno := mut.Commit()
	if errno != 0 {
		snap.lock.Lock()
		snap.lastErrno = errno
		snap.lock.Unlock()
		log.Printf("Failed to commit upsert: %s\n", snap.lastErrno.Error())
		return
	}
	errno = snap.sr.WriteUpdate(mut.ToRootLogUpdate(newroot))
	if snap.lastErrno != 0 {
		snap.lock.Lock()
		snap.lastErrno = errno
		snap.lock.Unlock()
		log.Printf("Failed to commit upsert to rootlog: %s\n", snap.lastErrno.Error())
		return
	}
}

func (snap *Snapshot) GetRoot() BlockRef {
	snap.lock.Lock()
	root := snap.root
	snap.refcounts[root]++
	snap.lock.Unlock()
	return root
}

func (snap *Snapshot) ReleaseRoot(root BlockRef) {
	var deaths []BlockRef
	snap.lock.Lock()
	snap.refcounts[root]--
	if snap.refcounts[root] <= 0 {
		deaths = snap.deaths[root]
		delete(snap.deaths, root)
	}
	snap.lock.Unlock()
	for _, dead := range deaths {
		snap.sr.FreeBlock(dead.blockno)
	}
}

func (snap *Snapshot) Get(key Key) (KV, syscall.Errno) {
	root := snap.GetRoot()
	defer snap.ReleaseRoot(root)
	return snap.sr.Load(root).Get(key)

}

func (snap *Snapshot) GetNext(key Key) (KV, syscall.Errno) {
	root := snap.GetRoot()
	defer snap.ReleaseRoot(root)
	return snap.sr.Load(root).GetNext(key)
}

func (snap *Snapshot) GetData(inode, start uint64, dest []byte) ([]byte, syscall.Errno) {
	root := snap.GetRoot()
	defer snap.ReleaseRoot(root)
	end := start + uint64(len(dest))
	kvs, errno := snap.sr.Load(root).GetData(inode, start, end)
	if errno != 0 {
		return nil, errno
	}
	for _, kv := range kvs {
		dkv := kv.(*DataKV)
		if dkv.Data != nil {
			if start <= dkv.Start {
				copy(dest[dkv.Start-start:], dkv.Data)
			} else {
				copy(dest, dkv.Data[start-dkv.Start:])
			}
		} else {
			clear(dest[dkv.Start-start : dkv.End-start])
		}
	}
	return dest, 0
}

func (m *SnapshotMutator) NewBlockAllocator() *BlockAllocator {
	return NewBlockAllocator(m.Superblock)
}

func (m *SnapshotMutator) AllocBlock() uint64 {
	if len(m.zombies) > 0 {
		// there's a zombie so let's not go to the allocator
		for blockno := range m.zombies {
			log.Printf("Reusing zombie block %d\n", blockno)
			delete(m.zombies, blockno)
			m.births[blockno] = true
			return blockno
		}
	}
	blockno := m.parent.AllocBlock()
	m.births[blockno] = true
	return blockno
}

func (m *Snapshot) AllocInode() uint64 {
	return m.sr.AllocInode()
}

func (m *Snapshot) clearCache() {
	m.sr.clearCache()
}

func (m *SnapshotMutator) Cache(ref BlockRef, node Node) {
	m.parent.Cache(ref, node)
}

func (m *SnapshotMutator) Uncache(ref BlockRef) {
	m.parent.Uncache(ref)
}

func (m *SnapshotMutator) Append(ref BlockRef, seg Segment, new Node) BlockKV {
	a := m.NewBlockAllocator()
	seg.Serialize(a)
	block := a.ToWriter(SegmentSig)
	seg.Serialize(block)
	block.Finish()
	offset := ref.blockno*uint64(m.BlockSize) + uint64(ref.size)
	//	log.Printf("Appending %d bytes to %v at location 0x%x", block.Size(), ref, offset)
	ref.size += block.Size()
	m.pending[offset] = block.data
	m.Cache(ref, new)
	return BlockKV{new.Leftmost(), ref}
}

func (m *SnapshotMutator) Enqueue(node Node) BlockKV {
	a := m.NewBlockAllocator()
	node.Serialize(a)
	var sig uint32
	if node.NumChildren() != 0 {
		sig = BranchSig
	} else {
		sig = LeafSig
	}
	block := a.ToWriter(sig)
	node.Serialize(block)
	block.Finish()
	var blockno uint64 = m.AllocBlock()
	ref := BlockRef{blockno, m.gen, node.NumChildren(), block.Size()}
	log.Printf("Enqueuing new node %v of size %d at location 0x%x", ref, block.Size(), ref.blockno*uint64(m.BlockSize))
	m.pending[ref.blockno*uint64(m.BlockSize)] = block.data
	m.Cache(ref, node)
	return BlockKV{node.Leftmost(), ref}
}

func (m *SnapshotMutator) Delete(ref BlockRef) {
	log.Printf("Deleting %v\n", ref)
	if m.births[ref.blockno] {
		m.Uncache(ref)
		delete(m.pending, ref.blockno*uint64(m.BlockSize))
		delete(m.births, ref.blockno)
		m.zombies[ref.blockno] = true
		return
	}
	m.deaths = append(m.deaths, ref)
}

func (m *SnapshotMutator) IsDirty(ref BlockRef) bool {
	_, dirty := m.pending[ref.blockno*uint64(m.BlockSize)]
	return dirty
}

func (m *SnapshotMutator) Commit() syscall.Errno {
	for offset, data := range m.pending {
		errno := m.parent.Write(offset, data)
		if errno != 0 {
			return errno
		}
	}
	return m.parent.Flush()
}

func (m *SnapshotMutator) ToRootLogUpdate(newroot BlockRef) (entries []RootLogEntry) {
	for blockno := range m.births {
		entries = append(entries, &AllocEntry{blockno})
	}
	entries = append(entries, &NewRootEntry{newroot, m.gen})
	for _, ref := range m.deaths {
		if ref.birth == m.gen {
			entries = append(entries, &FreeEntry{ref.blockno})
		} else {
			entries = append(entries, &DeadlistEntry{ref.birth, m.gen, ref.blockno})
		}
	}
	return
}
