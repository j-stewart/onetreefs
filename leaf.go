package onetreefs

import (
	"log"
	"syscall"
)

type Leaf struct {
	data Segment
}

const LeafSig uint32 = 0x6661656C

func (l *Leaf) Upsert(seg Segment, ref BlockRef, a *SnapshotMutator) []BlockKV {
	updateSize := seg.Size(a)
	newseg := l.data.Coalesce(seg).Clean()
	if a.gen == ref.birth && updateSize+ref.size <= a.BlockSize {
		newref := a.Append(ref, seg, &Leaf{newseg})
		return []BlockKV{newref}
	}
	a.Delete(ref)
	if len(newseg) == 0 {
		return nil
	}
	newLeaf := &Leaf{newseg}
	sa := a.NewBlockAllocator()
	newLeaf.Serialize(sa)
	size := sa.Size()
	if size > a.BlockSize {
		segs := newseg.Split(a)
		result := make([]BlockKV, len(segs))
		for i, seg := range segs {
			result[i] = a.Enqueue(&Leaf{seg})

		}
		log.Printf("Splitting leaf %v into %v\n", ref, result)
		return result
	}
	return []BlockKV{a.Enqueue(newLeaf)}
}

func (l *Leaf) Leftmost() Key {
	if len(l.data) > 0 {
		return l.data[0].Key()
	} else {
		// the minimum key
		return &AttrKey{0, ""}
	}
}

func (l *Leaf) NumChildren() uint32 {
	return 0
}

func (l *Leaf) Get(key Key) (KV, syscall.Errno) {
	kv := l.data.Get(key)
	if kv != nil && kv.IsTombstone() {
		return nil, 0
	}
	return kv, 0
}

func (l *Leaf) GetNext(key Key) (KV, syscall.Errno) {
	return l.data.GetNext(key), 0
}

func (l *Leaf) GetData(inode, start, end uint64) ([]KV, syscall.Errno) {
	return l.data.GetData(inode, start, end), 0
}

func (l *Leaf) Serialize(b DataWriter) {
	l.data.Serialize(b)
}
