package onetreefs

// a sorted list of KVs; a piece of a log

type Segment []KV

const SegmentSig uint32 = 0x6D676573

func (seg Segment) Partition(pivot []BlockKV) (result []Segment) {
	i := 0
	for _, k := range pivot[1:] {
		start := i
		for i < len(seg) && CmpKeys(seg[i].Key(), k.key) < 0 {
			i++
		}
		result = append(result, seg[start:i])
	}
	result = append(result, seg[i:])
	return
}

func (seg Segment) Split(m *SnapshotMutator) (segs []Segment) {
	size := seg.Size(m)
	min_segs := size/m.BlockSize + 1
	target := size / min_segs
	for i := 0; i < len(seg); {
		a := m.NewBlockAllocator()
		newseg := make([]KV, 0, len(seg)/int(min_segs))
		for a.Size() < target && i < len(seg) {
			newseg = append(newseg, seg[i])
			seg[i].Serialize(a)
			i++
		}
		segs = append(segs, newseg)
	}
	return
}

func (seg Segment) Coalesce(other Segment) Segment {
	i, j := 0, 0
	result := make([]KV, 0, len(seg)+len(other))
	// TODO join adjacent KVs
	for i < len(seg) && j < len(other) {
		if seg[i].Overlaps(other[j]) {
			result = append(result, seg[i].Merge(other[j])...)
			i++
			j++
		} else if CmpKeys(seg[i].Key(), other[j].Key()) < 0 {
			result = append(result, seg[i])
			i++
		} else {
			result = append(result, other[j])
			j++
		}
	}
	for i < len(seg) {
		result = append(result, seg[i])
		i++
	}
	for j < len(other) {
		result = append(result, other[j])
		j++
	}
	return result
}

func (seg Segment) Clean() Segment {
	result := make([]KV, 0, len(seg))
	for _, kv := range seg {
		if !kv.IsTombstone() {
			result = append(result, kv)
		}
	}
	return result
}

func (seg Segment) Get(key Key) KV {
	for _, kv := range seg {
		switch CmpKeys(kv.Key(), key) {
		case 0:
			return kv
		case 1:
			// we've overshot
			return nil
		}
	}
	return nil
}

func (seg Segment) GetNext(key Key) KV {
	for _, kv := range seg {
		if !kv.IsTombstone() && CmpKeys(kv.Key(), key) > 0 {
			return kv
		}
	}
	return nil
}

func (seg Segment) GetData(inode, start, end uint64) (result []KV) {
	probe := &DataKV{inode, start, end, nil}
	for _, kv := range seg {
		if kv.Overlaps(probe) {
			result = append(result, kv)
		}
	}
	return
}

func (seg Segment) Size(m *SnapshotMutator) uint32 {
	a := m.NewBlockAllocator()
	seg.Serialize(a)
	return a.Size()
}

func (seg Segment) Serialize(b DataWriter) {
	b.Write64(uint64(len(seg)))
	for _, kv := range seg {
		kv.Serialize(b)
	}
}

func DeserializeSegment(b *BlockReader) Segment {
	length := int(b.Read64())
	seg := make([]KV, length)
	for i := range length {
		seg[i] = DeserializeKV(b)
	}
	return seg
}
