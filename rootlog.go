package onetreefs

import (
	"errors"
	"log"
	"sync"
	"syscall"
	"time"
)

type RootLog struct {
	*Superblock
	snapshots     map[string]uint32
	roots         map[uint32]BlockRef
	freeBlocks    map[uint64]bool
	maxgen        uint32
	lastFreeBlock uint64 // TODO track lastAllocatedBlock/Inode
	totalFree     uint64
	lastFreeInode uint64

	superblockSize uint32
	dev            BlockDevice
	logTail        uint64
	logAge         uint32

	lock sync.Mutex
}

type RootLogEntry interface {
	Replay(*RootLog)
	Serialize(b DataWriter)
}

func EmptyRootLog(dev BlockDevice, su *Superblock, superblockSize uint32) *RootLog {
	rootlogBlocks := uint64((su.RootlogRes + su.BlockSize - 1) / su.BlockSize)
	return &RootLog{
		Superblock:     su,
		lastFreeBlock:  rootlogBlocks,
		totalFree:      su.FsSize - rootlogBlocks,
		superblockSize: superblockSize,
		dev:            dev,
		logTail:        uint64(superblockSize),
		snapshots:      make(map[string]uint32),
		roots:          make(map[uint32]BlockRef),
		freeBlocks:     make(map[uint64]bool),
	}
}

func ReplayRootLog(dev BlockDevice, su *Superblock, superblockSize uint32) (*RootLog, syscall.Errno) {
	var offset uint64 = uint64(superblockSize)
	rl := EmptyRootLog(dev, su, superblockSize)
	rl.totalFree = su.FsSize
	for offset < uint64(su.RootlogRes) {
		b, size, errno := ReadLog(dev, offset, RootLogSig)
		offset += uint64(size)
		if errno != 0 {
			return nil, errno
		}
		if size == 0 {
			// got to a 0 entry
			break
		}
		b.ReadPadding(16) // skip the timestamp
		age := b.Read32()
		if age < rl.logAge {
			// outdated; skip it
			continue
		}
		if age > rl.logAge {
			// supersedes what we've already read; clear everything
			rl = EmptyRootLog(dev, su, superblockSize)
			rl.logAge = age
		}
		count := int(b.Read32())
		for range count {
			rle := DeserializeRootLogEntry(b)
			rle.Replay(rl)
		}
		offset += uint64(size)
	}
	rl.logTail = offset
	return rl, 0
}

func (rl *RootLog) GetSnapshotByName(name string) (gen uint32, root BlockRef, err error) {
	rl.lock.Lock()
	defer rl.lock.Unlock()
	gen, ok := rl.snapshots[name]
	if !ok {
		err = errors.New("not found")
		return
	}
	root, ok = rl.roots[gen]
	if !ok {
		err = errors.New("gen not found")
		return
	}
	return
}

// TODO neither add nor delete have the right semantics

func (rl *RootLog) AddSnapshot(name string, parent *Snapshot) (gen uint32, root BlockRef) {
	rl.lock.Lock()
	defer rl.lock.Unlock()
	rl.maxgen += 1
	rl.snapshots[name] = rl.maxgen
	rl.roots[rl.maxgen] = parent.root
	rl.WriteUpdate([]RootLogEntry{
		&NewSnapshotEntry{name, rl.maxgen},
		&NewRootEntry{parent.root, rl.maxgen}})
	return rl.maxgen, root
}

func (rl *RootLog) DeleteSnapshot(name string) {
	rl.lock.Lock()
	defer rl.lock.Unlock()
	gen, ok := rl.snapshots[name]
	if !ok {
		// TODO maybe an error?
		return
	}
	delete(rl.snapshots, name)
	delete(rl.roots, gen)
	rl.WriteUpdate([]RootLogEntry{
		&DeadSnapshotEntry{name, gen}})
	return
}

func (rl *RootLog) AllocBlock() uint64 {
	rl.lock.Lock()
	defer rl.lock.Unlock()
	rl.totalFree--
	for blockno := range rl.freeBlocks {
		delete(rl.freeBlocks, blockno)
		return blockno
	}
	// TODO check against disk size
	blockno := rl.lastFreeBlock
	rl.lastFreeBlock++
	return blockno
}

func (rl *RootLog) FreeBlock(blockno uint64) {
	rl.lock.Lock()
	defer rl.lock.Unlock()
	rl.totalFree++
	if rl.lastFreeBlock == blockno {
		rl.lastFreeBlock--
		return
	}
	rl.freeBlocks[blockno] = true
}

func (rl *RootLog) AllocInode() uint64 {
	rl.lock.Lock()
	defer rl.lock.Unlock()
	inode := rl.lastFreeInode
	rl.lastFreeInode++
	return inode
}

const RootLogSig uint32 = 0x31415925

func (rl *RootLog) WriteUpdate(entries []RootLogEntry) syscall.Errno {
	a := NewBlockAllocator(rl.Superblock)
	t := time.Now()
	// this could be conditional only if it increases
	entries = append(entries, &AllocInodeEntry{rl.lastFreeInode - 1})
	rl.SerializeUpdate(a, t, entries)

	rl.lock.Lock()

	if rl.logTail < uint64(rl.RootlogRes)/2 && uint64(a.Size())+rl.logTail > uint64(rl.RootlogRes)/2 {
		log.Printf("Doing mid-rootlog summary\n")
		// we're gonna crosss halfway. Make this entry a summary
		rl.logAge++
		entries = rl.toEntries()
		a = NewBlockAllocator(rl.Superblock)
		rl.SerializeUpdate(a, t, entries)
	}
	if uint64(a.Size())+rl.logTail > uint64(rl.RootlogRes) {
		log.Printf("Restarting rootlog")
		rl.logTail = uint64(rl.superblockSize)
		rl.logAge++
		entries = rl.toEntries()
		a = NewBlockAllocator(rl.Superblock)
		rl.SerializeUpdate(a, t, entries)
	}
	if uint64(a.Size())+rl.logTail > uint64(rl.RootlogRes) {
		log.Fatal("Rootlog summary exceeds rootlog size")
	}

	dest := rl.logTail
	rl.logTail += uint64(a.Size())

	rl.lock.Unlock()

	w := a.ToWriter(RootLogSig)
	rl.SerializeUpdate(w, t, entries)
	w.Finish()

	errno := rl.dev.Write(dest, w.data)
	if errno != 0 {
		return errno
	}
	return 0
}

func (rl *RootLog) toEntries() (entries []RootLogEntry) {
	// must be called with rl.lock held
	entries = append(entries, &AllocInodeEntry{rl.lastFreeInode - 1})
	for name, gen := range rl.snapshots {
		entries = append(entries, &NewSnapshotEntry{name, gen})
	}
	for gen, root := range rl.roots {
		entries = append(entries, &NewRootEntry{root, gen})
	}
	for i := range rl.lastFreeBlock {
		if !rl.freeBlocks[i] {
			entries = append(entries, &AllocEntry{uint64(i)})
		}
	}
	return
}

func (rl *RootLog) SerializeUpdate(w DataWriter, t time.Time, entries []RootLogEntry) {
	w.Write64(uint64(t.Unix()))
	w.Write64(uint64(t.Nanosecond()))
	w.Write32(rl.logAge)
	w.Write32(uint32(len(entries)))
	for _, e := range entries {
		e.Serialize(w)
	}
}

type DeadlistEntry struct {
	birth   uint32
	death   uint32
	blockno uint64
}

type AllocEntry struct {
	blockno uint64
}

type FreeEntry struct {
	blockno uint64
}

type NewRootEntry struct {
	block BlockRef
	gen   uint32
}

type NewSnapshotEntry struct {
	name string
	gen  uint32
}

type DeadSnapshotEntry struct {
	name string
	gen  uint32
}

type AllocInodeEntry struct {
	inode uint64
}

func (e *DeadlistEntry) Replay(rl *RootLog) {
	// TODO
}

func (e *AllocEntry) Replay(rl *RootLog) {
	rl.totalFree--
	if rl.lastFreeBlock >= e.blockno {
		for i := rl.lastFreeBlock; i < e.blockno; i++ {
			rl.freeBlocks[i] = true
		}
		rl.lastFreeBlock = e.blockno + 1
	}
	if rl.freeBlocks[e.blockno] {
		delete(rl.freeBlocks, e.blockno)
	}
}

func (e *FreeEntry) Replay(rl *RootLog) {
	rl.totalFree++
	if rl.lastFreeBlock-1 == e.blockno {
		rl.lastFreeBlock = e.blockno
	} else {
		rl.freeBlocks[e.blockno] = true
	}
}

func (e *NewRootEntry) Replay(rl *RootLog) {
	if rl.maxgen < e.block.birth {
		rl.maxgen = e.block.birth
	}
	rl.roots[e.block.birth] = e.block
}

func (e *NewSnapshotEntry) Replay(rl *RootLog) {
	rl.snapshots[e.name] = e.gen
}

func (e *DeadSnapshotEntry) Replay(rl *RootLog) {
	delete(rl.snapshots, e.name)
	// TODO clean up deadlists
}

func (e *AllocInodeEntry) Replay(rl *RootLog) {
	rl.lastFreeInode = max(rl.lastFreeInode, e.inode+1)
}

const (
	AllocEntrySig uint64 = iota
	FreeEntrySig
	DeadlistEntrySig
	NewRootEntrySig
	NewSnapshotEntrySig
	DeadSnapshotEntrySig
	AllocInodeEntrySig
)

func (e *AllocEntry) Serialize(b DataWriter) {
	b.Write64(AllocEntrySig)
	b.Write64(e.blockno)
}

func (e *FreeEntry) Serialize(b DataWriter) {
	b.Write64(FreeEntrySig)
	b.Write64(e.blockno)
}

func (e *DeadlistEntry) Serialize(b DataWriter) {
	b.Write64(DeadlistEntrySig)
	b.Write32(e.birth)
	b.Write32(e.death)
	b.Write64(e.blockno)
}

func (e *NewRootEntry) Serialize(b DataWriter) {
	b.Write64(NewRootEntrySig)
	e.block.Serialize(b)
	b.Write32(e.gen)
}

func (e *NewSnapshotEntry) Serialize(b DataWriter) {
	b.Write64(NewSnapshotEntrySig)
	b.Write32(e.gen)
	b.Write32(uint32(len(e.name)))
	b.WriteData([]byte(e.name))
}

func (e *DeadSnapshotEntry) Serialize(b DataWriter) {
	b.Write64(DeadSnapshotEntrySig)
	b.Write32(e.gen)
	b.Write32(uint32(len(e.name)))
	b.WriteData([]byte(e.name))
}

func (e *AllocInodeEntry) Serialize(b DataWriter) {
	b.Write64(AllocInodeEntrySig)
	b.Write64(e.inode)
}

func DeserializeRootLogEntry(b *BlockReader) RootLogEntry {
	switch b.Read64() {
	case AllocEntrySig:
		return &AllocEntry{b.Read64()}
	case FreeEntrySig:
		return &FreeEntry{b.Read64()}
	case DeadlistEntrySig:
		return &DeadlistEntry{b.Read32(), b.Read32(), b.Read64()}
	case NewRootEntrySig:
		return &NewRootEntry{DeserializeBlockRef(b), b.Read32()}
	case NewSnapshotEntrySig:
		gen := b.Read32()
		namelen := b.Read32()
		name := string(b.ReadData(namelen))
		return &NewSnapshotEntry{name, gen}
	case DeadSnapshotEntrySig:
		gen := b.Read32()
		namelen := b.Read32()
		name := string(b.ReadData(namelen))
		return &DeadSnapshotEntry{name, gen}
	case AllocInodeEntrySig:
		return &AllocInodeEntry{b.Read64()}
	}
	return nil
}
