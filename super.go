package onetreefs

import (
	"log"
	"syscall"
)

// all the static attributes of the filesystem set at format time
type Superblock struct {
	Version   uint32 // currently, 1
	BlockSize uint32
	// blockSize is the maximum size of a btree node on disk. A good
	// choice is probably (a multiple of) whatever the erase block
	// size is of the underlying SSD. It must be a multiple of segSize
	// and at least 2*segSize. Ideally it's much larger than segSize,
	// say 32-256 times. I designed with 4 MiB in mind.
	SegSize uint32
	// Most writes to disk happen in multiples of segSize. A good
	// choice is (a multiple of) the underylying SSD's block size to
	// write without a read-modify-write cycle. It should be at most
	// half blockSize and at least twice the pageSize. Ideally it's
	// much larger than pageSize, say 4-64 times. I designed with 64
	// KiB in mind.
	PageSize uint32
	// Files are tracked in chunks of pageSize. This is what most
	// filesystems consider a blocksize. In Linux at least the natural
	// file block size is probably one VM page, hence the name. This
	// could be any positive integer up to one half segSize. I
	// designed with 4 KiB in mind.
	MaxPivot int
	// The maximum number of child pointers in a branch (pivot)
	// node. The maximum number should serialize to less than 1/2
	// block or 1 block - 1 segment. The more pivot pointers the wider
	// and shallower the tree, which should be faster for reads
	// although log flushes of branch nodes may be slower. I designed
	// with approximately one segSize worth of pivot pointers, or in
	// round numbers 256.
	RootlogRes uint32
	// The amount of bytes to reserve for the root log. In practice
	// anything less than 1 blockSize will be wasted. 2 blocks is what
	// I had in mind when designing.
	FsSize uint64
	// in blocks of blockSize. Things go really sideways when a write
	// (even to delete) happens on a full filesystem. I'm not sure
	// what the minimum is but probably at least 32?
}

type SuperRoot struct {
	Superblock
	dev   BlockDevice
	log   *RootLog
	cache map[BlockRef]Node
}

type Loader interface {
	Load(BlockRef) Node
	Cache(BlockRef, Node)
	Uncache(BlockRef)
}

type Mutator interface {
	Loader
	AllocBlock() uint64
	FreeBlock(uint64)
	WriteUpdate([]RootLogEntry) syscall.Errno
	Write(uint64, []byte) syscall.Errno
	Flush() syscall.Errno
}

const SuperblockSig uint32 = 0x74656e6f
const SuperblockSize = 48

func (sb Superblock) Serialize(b DataWriter) {
	b.Write32(sb.Version)
	b.Write32(sb.BlockSize)
	b.Write32(sb.SegSize)
	b.Write32(sb.PageSize)
	b.Write32(uint32(sb.MaxPivot))
	b.Write32(sb.RootlogRes)
	b.Write64(sb.FsSize)
}

func (sb Superblock) Write(dev BlockDevice) (syscall.Errno, uint32) {
	a := NewBlockAllocator(&sb)
	sb.Serialize(a)
	b := a.ToWriter(SuperblockSig)
	sb.Serialize(b)
	b.Finish()
	return dev.Write(0, b.data), b.Size()
}

func ReadSuperblock(dev BlockDevice) (sr *Superblock, size uint32) {
	b, size, errno := ReadLog(dev, 0, SuperblockSig)
	if errno != 0 {
		log.Printf("Error reading superblock: %s\n", errno.Error())
		return nil, 0
	}
	if size < SuperblockSize {
		log.Printf("Error reading superblock: %s\n", errno.Error())
		return nil, 0
	}
	sr = new(Superblock)
	sr.Version = b.Read32()
	if sr.Version != 1 {
		log.Printf("Bad superblock version %d\n", sr.Version)
		return nil, 0
	}
	sr.BlockSize = b.Read32()
	sr.SegSize = b.Read32()
	sr.PageSize = b.Read32()
	sr.MaxPivot = int(b.Read32())
	sr.RootlogRes = b.Read32()
	sr.FsSize = b.Read64()
	return
}

func (sr *SuperRoot) NewSnapshot(name string, gen uint32, root BlockRef) (snap *Snapshot) {
	return &Snapshot{name: name, gen: gen, sr: sr, root: root,
		refcounts: make(map[BlockRef]int), deaths: make(map[BlockRef][]BlockRef)}
}

func (sr *SuperRoot) GetSnapshotByName(name string) *Snapshot {
	gen, root, err := sr.log.GetSnapshotByName(name)
	if err != nil {
		// TODO error handling
		return nil
	}
	return sr.NewSnapshot(name, gen, root)
}

func (sr *SuperRoot) Cache(ref BlockRef, node Node) {
	sr.cache[ref] = node
}

func (sr *SuperRoot) Uncache(ref BlockRef) {
	delete(sr.cache, ref)
}

func (sr *SuperRoot) clearCache() {
	// for testing
	sr.cache = make(map[BlockRef]Node)
}

func (sr *SuperRoot) AllocBlock() uint64 {
	return sr.log.AllocBlock()
}

func (sr *SuperRoot) FreeBlock(blockno uint64) {
	sr.log.FreeBlock(blockno)
}

func (sr *SuperRoot) AllocInode() uint64 {
	return sr.log.AllocInode()
}

func (sr *SuperRoot) Load(ref BlockRef) Node {
	if node, present := sr.cache[ref]; present {
		return node
	}
	log.Printf("Loading block %d (%d bytes)\n", ref.blockno, ref.size)
	data, err := sr.dev.Read(ref.blockno*uint64(sr.BlockSize), ref.size)
	if err != 0 {
		log.Printf("Error loading block %d\n", ref.blockno)
		return &ErrorNode{err}
	}
	node := DeserializeNode(sr, data)
	sr.cache[ref] = node
	return node
}

func (sr *SuperRoot) Write(offset uint64, data []byte) syscall.Errno {
	return sr.dev.Write(offset, data)
}

func (sr *SuperRoot) WriteUpdate(entries []RootLogEntry) syscall.Errno {
	return sr.log.WriteUpdate(entries)
}

func (sr *SuperRoot) Flush() syscall.Errno {
	return sr.dev.Flush()
}

func FormatDevice(dev BlockDevice, sb Superblock, rootdirUpdate []KV) syscall.Errno {
	err, size := sb.Write(dev)
	if err != 0 {
		return err
	}
	log.Printf("wrote superblock of size %d\n", size)
	// write 0's to reserve the rest of the rootlog space
	zeroes := make([]byte, sb.RootlogRes-size)
	dev.Write(uint64(size), zeroes)

	rootlog := EmptyRootLog(dev, &sb, size)
	rootlog.logAge = 1
	rootlog.lastFreeInode = 2 // rootdir inode is 1
	var blockno uint64 = rootlog.lastFreeBlock
	rootlog.lastFreeBlock++
	root := &Leaf{rootdirUpdate}
	a := NewBlockAllocator(&sb)
	root.Serialize(a)
	w := a.ToWriter(LeafSig)
	root.Serialize(w)
	w.Finish()
	err = dev.Write(uint64(sb.BlockSize)*blockno, w.data)
	if err != 0 {
		return err
	}
	rootRef := BlockRef{blockno, 1, 0, w.Size()}
	err = rootlog.WriteUpdate([]RootLogEntry{
		&AllocEntry{blockno},
		&NewRootEntry{rootRef, 1},
		&NewSnapshotEntry{"", 1}})
	if err != 0 {
		return err
	}
	return 0
}

func formatForTest(sb Superblock) BlockDevice {
	dev := CreateRamDevice()
	errno := FormatDevice(dev, sb, nil)
	if errno != 0 {
		log.Fatal("Failed to create superblock: ", errno)
	}
	return dev
}

var testSuperblock = Superblock{
	Version:    1,
	BlockSize:  65536,
	SegSize:    8192,
	PageSize:   4096,
	MaxPivot:   32,
	RootlogRes: 16384,
	FsSize:     256,
}

func createForTest(sb Superblock) *Snapshot {
	dev := formatForTest(sb)
	super, errno := ReadSuperRoot(dev)
	if errno != 0 {
		log.Fatal("Failed to read superblock: ", errno)
	}
	snap := super.GetSnapshotByName("")
	if snap == nil {
		log.Fatal("No default snapshot found\n")
	}
	return snap
}

func ReadSuperRoot(dev BlockDevice) (*SuperRoot, syscall.Errno) {
	sb, size := ReadSuperblock(dev)
	if sb == nil {
		return nil, syscall.EIO
	}
	rootlog, err := ReplayRootLog(dev, sb, size)
	if err != 0 {
		return nil, err
	}
	return &SuperRoot{*sb, dev, rootlog, make(map[BlockRef]Node)}, 0
}
