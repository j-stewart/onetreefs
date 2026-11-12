package onetreefs

import (
	"log"
	"syscall"
)

type Branch struct {
	loader Loader
	pivot  []BlockKV
	log    Segment
}

const BranchSig uint32 = 0x65657274

func (b *Branch) Upsert(seg Segment, ref BlockRef, m *SnapshotMutator) []BlockKV {
	updateSize := seg.Size(m)
	if updateSize+ref.size > m.BlockSize {
		m.Delete(ref)
		result := b.Rewrite(seg, m)
		log.Printf("Replacing branch %v with %v\n", ref, result)
		return result
	}
	// can't append because we don't own it
	if m.gen != ref.birth {
		m.Delete(ref)
		return b.Rewrite(seg, m)
	}
	return []BlockKV{m.Append(ref, seg,
		&Branch{b.loader, b.pivot, b.log.Coalesce(seg)})}
}

func (b *Branch) Rewrite(seg Segment, m *SnapshotMutator) []BlockKV {
	newlog := b.log.Coalesce(seg)
	sa := m.NewBlockAllocator()
	newBranch := &Branch{b.loader, b.pivot, newlog}
	newBranch.Serialize(sa)
	if sa.Size() < m.BlockSize*3/4 {
		// small enough we can just write
		// require that we've shrunk a fair bit if we're going to just rewrite
		return []BlockKV{m.Enqueue(newBranch)}
	}
	// we're too big; we have to spill our log
	newkids := make([]BlockKV, 0, len(b.pivot))
	for i, l := range newlog.Partition(b.pivot) {
		if len(l) > 0 {
			newkids = append(newkids,
				b.loader.Load(b.pivot[i].block).Upsert(l, b.pivot[i].block, m)...)
		} else {
			newkids = append(newkids, b.pivot[i])
		}
	}
	if len(newkids) == 0 {
		// oops, we've deleted everything
		return nil
	}

	// TODO rebalance here

	if len(newkids) > m.MaxPivot {
		mid := len(b.pivot) / 2
		leftPivot, rightPivot := newkids[:mid], newkids[mid:]
		return []BlockKV{
			m.Enqueue(&Branch{b.loader, leftPivot, nil}),
			m.Enqueue(&Branch{b.loader, rightPivot, nil})}
	}
	return []BlockKV{m.Enqueue(&Branch{b.loader, newkids, nil})}
}

func (b *Branch) Leftmost() Key {
	return b.pivot[0].key
}

func (b *Branch) NumChildren() uint32 {
	return uint32(len(b.pivot))
}

func (b *Branch) FindChild(probe Key) int {
	for i := 0; i < len(b.pivot)-1; i++ {
		if i+1 == len(b.pivot) || CmpKeys(probe, b.pivot[i+1].key) < 0 {
			return i
		}
	}
	return len(b.pivot) - 1
}

func (b *Branch) Serialize(block DataWriter) {
	// write the pivot
	// then write the log
	block.Write64(uint64(len(b.pivot)))
	for _, kv := range b.pivot {
		kv.key.Serialize(block)
		kv.block.Serialize(block)
	}
	b.log.Serialize(block)
}

func DeserializePivot(block *BlockReader) []BlockKV {
	pivotlen := int(block.Read64())
	pivot := make([]BlockKV, pivotlen)
	for i := range pivotlen {
		pivot[i].key = DeserializeKey(block)
		pivot[i].block = DeserializeBlockRef(block)
	}
	return pivot
}

func (b *Branch) Get(key Key) (KV, syscall.Errno) {
	result := b.log.Get(key)
	if result != nil {
		if result.IsTombstone() {
			return nil, 0
		}
		return result, 0
	}

	kid := b.loader.Load(b.pivot[b.FindChild(key)].block)
	return kid.Get(key)
}

func (b *Branch) GetNext(key Key) (KV, syscall.Errno) {
	logResult := b.log.GetNext(key)
	for {
		childIndex := b.FindChild(key)
		child := b.loader.Load(b.pivot[childIndex].block)
		childResult, errno := child.GetNext(key)
		if errno != 0 {
			return nil, errno
		}
		if childResult != nil {
			// if our log result is better just return it
			if logResult != nil && CmpKeys(logResult.Key(), childResult.Key()) < 0 {
				return logResult, 0
			}
			// gotta check if the child got killed in our log
			maybeTombstone := b.log.Get(childResult.Key())
			if maybeTombstone != nil && maybeTombstone.IsTombstone() {
				// if so, try that key's successor
				key = childResult.Key()
				continue
			}
			return childResult, 0
		}
		// no child result so whatever's in the log is next
		return logResult, 0
	}
}

func (b *Branch) GetData(inode, start, end uint64) (result []KV, errno syscall.Errno) {
	startIndex := b.FindChild(&DataKey{inode, start})
	endIndex := b.FindChild(&DataKey{inode, end})
	for i := startIndex; i <= endIndex; i++ {
		kid := b.loader.Load(b.pivot[i].block)
		if kid != nil {
			kidData, errno := kid.GetData(inode, start, end)
			if errno != 0 {
				return nil, errno
			}
			result = append(result, kidData...)
		}
	}

	result = append(result, b.log.GetData(inode, start, end)...)
	return
}
