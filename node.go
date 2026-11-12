package onetreefs

import (
	"log"
	"syscall"
)

type Node interface {
	Leftmost() Key
	NumChildren() uint32
	Get(Key) (KV, syscall.Errno)
	GetNext(Key) (KV, syscall.Errno)
	GetData(inode, start, end uint64) ([]KV, syscall.Errno)
	Upsert(Segment, BlockRef, *SnapshotMutator) []BlockKV
	Serialize(DataWriter)
}

func DeserializeNode(loader Loader, b *BlockReader) Node {
	sig, chunk := b.ReadLog()
	var pivot []BlockKV
	var data Segment
	switch sig {
	case LeafSig:
		data = DeserializeSegment(chunk)
	case BranchSig:
		pivot = DeserializePivot(chunk)
		data = DeserializeSegment(chunk)
	default:
		// TODO better errno
		return &ErrorNode{syscall.EIO}
	}
	for b.HasData() {
		segsig, chunk := b.ReadLog()
		if segsig != SegmentSig {
			log.Printf("bad segment deserializing node: 0x%x\n", segsig)
			// TODO better error, or maybe just optimistically skip?
			return &ErrorNode{syscall.EIO}
		}
		data = data.Coalesce(DeserializeSegment(chunk))
	}
	switch sig {
	case LeafSig:
		return &Leaf{data}
	case BranchSig:
		return &Branch{loader, pivot, data}
	}
	// TODO better errno
	return &ErrorNode{syscall.EIO}
}

type ErrorNode struct {
	errno syscall.Errno
}

func (en *ErrorNode) Leftmost() Key {
	return nil
}

func (en *ErrorNode) NumChildren() uint32 {
	return 0
}

func (en *ErrorNode) Get(key Key) (KV, syscall.Errno) {
	return nil, en.errno
}

func (en *ErrorNode) GetNext(key Key) (KV, syscall.Errno) {
	return nil, en.errno
}

func (en *ErrorNode) GetData(inode, start, end uint64) ([]KV, syscall.Errno) {
	return nil, en.errno
}

func (en *ErrorNode) Upsert(Segment, BlockRef, *SnapshotMutator) []BlockKV {
	log.Fatalln("Tried to upsert an error node")
	return nil
}

func (en *ErrorNode) Serialize(w DataWriter) {
	log.Fatalln("Tried to serialize an error node")
}
