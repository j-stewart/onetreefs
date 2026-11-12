package onetreefs

import (
	"cmp"
	"hash/fnv"
	"math"
)

type Key interface {
	Ino() uint64
	Serialize(b DataWriter)
}

type KV interface {
	Key() Key
	Overlaps(other KV) bool
	Merge(other KV) []KV
	IsTombstone() bool
	Serialize(b DataWriter)
}

type DataKV struct {
	// Key
	Inode uint64
	Start uint64
	End   uint64
	// Value
	// nil means tombstone
	Data []byte
}

type DirKV struct {
	// Key
	Inode    uint64
	Hash     uint64
	Filename string
	// Value
	FileInode uint64 // 0 for deleted
	Mode      byte
}

type AttrKV struct {
	// Key
	Inode uint64
	Name  string // 0-length name means the stat entry
	// Value
	// nil means tombstone; 0-length value is allowed
	Data []byte
}

type DataKey struct {
	Inode  uint64
	Offset uint64
}

type DirKey struct {
	Inode    uint64
	Hash     uint64
	Filename string
}

type AttrKey struct {
	Inode uint64
	Name  string
}

func HashFilename(filename string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(filename))
	sum := h.Sum64()
	// hash must fit in a signed int and must not be zero
	if sum > math.MaxInt64 {
		sum = sum >> 1
	}
	if sum == 0 {
		sum = 1
	}
	return sum
}

// We sort AttrKey < DataKey < DirKey
// (though you'll never get data and dir kvs for the same inode)

func CmpKeys(a, b Key) int {
	if a.Ino() != b.Ino() {
		return cmp.Compare(a.Ino(), b.Ino())
	}
	switch ka := a.(type) {
	case *AttrKey:
		switch kb := b.(type) {
		case *AttrKey:
			return cmp.Compare(ka.Name, kb.Name)
		case *DataKey:
			// AttrKey sorts before DataKey
			return -1
		case *DirKey:
			// AttrKey sorts before DirKey
			return -1
		}
	case *DataKey:
		switch kb := b.(type) {
		case *AttrKey:
			return 1
		case *DataKey:
			return cmp.Compare(ka.Offset, kb.Offset)
		case *DirKey:
			return -1
		}
	case *DirKey:
		switch kb := b.(type) {
		case *AttrKey:
			return 1
		case *DataKey:
			return 1
		case *DirKey:
			return cmp.Or(cmp.Compare(ka.Hash, kb.Hash), cmp.Compare(ka.Filename, kb.Filename))
		}
	}
	panic("unreachable")
}

func Lt(a, b Key) bool {
	return CmpKeys(a, b) < 0
}

func (k *AttrKey) Ino() uint64 {
	return k.Inode
}

func (k *DataKey) Ino() uint64 {
	return k.Inode
}

func (k *DirKey) Ino() uint64 {
	return k.Inode
}

func (kv *DataKV) Overlaps(other KV) bool {
	switch o := other.(type) {
	case *DataKV:
		return kv.Inode == o.Inode && max(kv.Start, o.Start) < min(kv.End, o.End)
	case *DirKV:
		return false
	case *AttrKV:
		return false
	}
	panic("unreachable")
}

func (kv *DirKV) Overlaps(other KV) bool {
	switch o := other.(type) {
	case *DataKV:
		return false
	case *DirKV:
		return kv.Inode == o.Inode && kv.Hash == o.Hash && kv.Filename == o.Filename
	case *AttrKV:
		return false
	}
	panic("unreachable")
}

func (kv *AttrKV) Overlaps(other KV) bool {
	switch o := other.(type) {
	case *DataKV:
		return false
	case *DirKV:
		return false
	case *AttrKV:
		return kv.Inode == o.Inode && kv.Name == o.Name
	}
	panic("unreachable")
}

func (kv *DataKV) Slice(newstart, newend uint64) KV {
	if kv.Data == nil {
		return &DataKV{kv.Inode, newstart, newend, nil}
	}
	datastart := newstart - kv.Start
	dataend := newend - kv.Start
	return &DataKV{kv.Inode, newstart, newend, kv.Data[datastart:dataend]}
}

func (kv *DataKV) Merge(other KV) []KV {
	if !kv.Overlaps(other) {
		if CmpKeys(kv.Key(), other.Key()) < 0 {
			return []KV{kv, other}
		} else {
			return []KV{other, kv}
		}
	}
	o := other.(*DataKV)
	if kv.Start >= o.Start && kv.End <= o.End {
		// kv    |-----|
		//  o |-----------|
		// just take newer
		return []KV{other}
	} else if kv.Start < o.Start && kv.End > o.End {
		// kv |-----------|
		//  o    |-----|
		// slice into 3
		if kv.Data == nil && o.Data == nil {
			return []KV{kv}
		} else if kv.Data != nil && o.Data != nil {
			newdata := append(kv.Data[:o.Start-kv.Start], o.Data...)
			newdata = append(newdata, kv.Data[:o.End-kv.Start]...)
			return []KV{&DataKV{kv.Inode, kv.Start, kv.End, newdata}}
		} else {
			return []KV{kv.Slice(kv.Start, o.Start), other, kv.Slice(o.End, kv.End)}
		}
	} else if kv.Start < o.Start && kv.End <= o.End {
		// kv |-----|
		//  o    |-----|
		if kv.Data == nil && o.Data == nil {
			return []KV{&DataKV{kv.Inode, kv.Start, o.End, nil}}
		} else if kv.Data != nil && o.Data != nil {
			return []KV{&DataKV{kv.Inode, kv.Start, o.End, append(kv.Data[:o.Start-kv.Start], o.Data...)}}
		} else {
			return []KV{kv.Slice(kv.Start, o.Start), other}
		}
	} else {
		// kv    |-----|
		//  o |-----|
		if kv.Data == nil && o.Data == nil {
			return []KV{&DataKV{kv.Inode, o.Start, kv.End, nil}}
		} else if kv.Data != nil && o.Data != nil {
			return []KV{&DataKV{kv.Inode, o.Start, kv.End, append(o.Data, kv.Data[o.End-kv.Start:]...)}}
		} else {
			return []KV{other, kv.Slice(o.End, kv.End)}
		}
	}
}

func (kv *DirKV) Merge(other KV) []KV {
	if !kv.Overlaps(other) {
		if CmpKeys(kv.Key(), other.Key()) < 0 {
			return []KV{kv, other}
		} else {
			return []KV{other, kv}
		}
	}
	return []KV{other}
}

func (kv *AttrKV) Merge(other KV) []KV {
	if !kv.Overlaps(other) {
		if CmpKeys(kv.Key(), other.Key()) < 0 {
			return []KV{kv, other}
		} else {
			return []KV{other, kv}
		}
	}
	return []KV{other}
}

func (kv *DataKV) Key() Key {
	return &DataKey{kv.Inode, kv.Start}
}

func (kv *DirKV) Key() Key {
	return &DirKey{kv.Inode, kv.Hash, kv.Filename}
}

func (kv *AttrKV) Key() Key {
	return &AttrKey{kv.Inode, kv.Name}
}

func (kv *DataKV) IsTombstone() bool {
	return kv.Data == nil
}

func (kv *DirKV) IsTombstone() bool {
	return kv.FileInode == 0
}

func (kv *AttrKV) IsTombstone() bool {
	return kv.Data == nil
}

const (
	DataKVTombstoneSig uint8 = iota
	DataKVDataSig
	DirKVSig
	AttrKVTombstoneSig
	AttrKVDataSig
)

const (
	DataKeySig uint8 = iota
	DirKeySig
	AttrKeySig
)

func (kv *DataKV) Serialize(b DataWriter) {
	b.Write64(kv.Inode) // 8
	b.Write64(kv.Start) // 16
	if kv.Data == nil {
		b.Write8(DataKVTombstoneSig)
		b.WritePadding(3)
		// TODO check bounds
		b.Write32(uint32(kv.End - kv.Start)) // 24
		b.WritePadding(8)                    // 32
	} else {
		b.Write8(DataKVDataSig)
		b.WritePadding(3)
		b.Write32(uint32(kv.End - kv.Start)) // 24
		b.WriteData(kv.Data)
		b.WritePadding(4) // 32
	}
}

func (kv *DirKV) Serialize(b DataWriter) {
	b.Write64(kv.Inode) // 8
	b.Write64(kv.Hash)  // 16
	b.Write8(DirKVSig)
	b.Write8(uint8(len(kv.Filename)))
	b.Write8(kv.Mode)
	b.WritePadding(1)
	b.WriteData([]byte(kv.Filename)) // 24
	b.Write64(kv.FileInode)          // 32
}

func (kv *AttrKV) Serialize(b DataWriter) {
	b.Write64(kv.Inode) // 8
	b.Write64(0)        // 16
	if kv.Data == nil {
		b.Write8(AttrKVTombstoneSig)
		b.Write8(uint8(len(kv.Name)))
		b.WritePadding(2)
		b.WriteData([]byte(kv.Name)) // 24
		b.WritePadding(8)
	} else {
		b.Write8(AttrKVDataSig)
		b.Write8(uint8(len(kv.Name)))
		b.Write16(uint16(len(kv.Data)))
		b.WriteData([]byte(kv.Name)) // 24
		b.WriteData(kv.Data)
		b.WritePadding(4)
	}
}

func (k *DataKey) Serialize(b DataWriter) {
	b.Write64(k.Inode)  // 8
	b.Write64(k.Offset) // 16
	b.Write8(DataKeySig)
	b.WritePadding(7) // 24
}

func (k *DirKey) Serialize(b DataWriter) {
	b.Write64(k.Inode) // 8
	b.Write64(k.Hash)  // 16
	b.Write8(DirKeySig)
	b.Write8(uint8(len(k.Filename)))
	b.WritePadding(2)
	b.WriteData([]byte(k.Filename)) // 24
}

func (k *AttrKey) Serialize(b DataWriter) {
	b.Write64(k.Inode) // 8
	b.Write64(0)       // 16
	b.Write8(AttrKeySig)
	b.Write8(uint8(len(k.Name)))
	b.WritePadding(2)
	b.WriteData([]byte(k.Name)) // 24
}

func BytesToKVs(snap *Snapshot, inode uint64, offset uint64, data []byte) (kvs []KV) {
	pagesize := snap.Superblock().PageSize
	return bytesToKVs(pagesize, inode, offset, data)
}

func bytesToKVs(pagesize uint32, inode uint64, offset uint64, data []byte) (kvs []KV) {
	p := uint64(pagesize)
	if len(data) == 0 {
		return nil
	}
	if offset%p != 0 {
		size := p - (offset % p)
		if uint64(len(data)) < size {
			chunk := make([]byte, len(data))
			copy(chunk, data)
			return []KV{&DataKV{inode, offset, offset + uint64(len(data)), chunk}}
		}
		chunk := make([]byte, size)
		copy(chunk, data)
		kvs = append(kvs, &DataKV{inode, offset, offset + size, chunk})
		offset += size
		data = data[size:]
	}
	for uint64(len(data)) >= p {
		chunk := make([]byte, pagesize)
		copy(chunk, data)
		kvs = append(kvs, &DataKV{inode, offset, offset + p, chunk})
		offset += p
		data = data[pagesize:]
	}
	if len(data) > 0 {
		chunk := make([]byte, len(data))
		copy(chunk, data)
		kvs = append(kvs, &DataKV{inode, offset, offset + uint64(len(data)), chunk})
	}
	return
}

func DataTombstones(snap *Snapshot, inode, start, end uint64) (kvs []KV) {
	pagesize := snap.Superblock().PageSize
	return dataTombstones(pagesize, inode, start, end)
}

func dataTombstones(pagesize uint32, inode, start, end uint64) (kvs []KV) {
	p := uint64(pagesize)
	if start > end {
		panic("invalid tombstone creation")
	}
	if start == end {
		return nil
	}
	if start%p != 0 {
		if start/p == (end-1)/p {
			return []KV{&DataKV{inode, start, end, nil}}
		}
		kvs = append(kvs, &DataKV{inode, start, (start/p + 1) * p, nil})
		start = (start/p + 1) * p
	}
	for end-start >= p {
		kvs = append(kvs, &DataKV{inode, start, start + p, nil})
		start += p
	}
	if end-start > 0 {
		kvs = append(kvs, &DataKV{inode, start, end, nil})
	}
	return
}

func DeserializeKV(b *BlockReader) KV {
	inode := b.Read64()
	second := b.Read64()
	switch b.Read8() {
	case DataKVTombstoneSig:
		b.ReadPadding(3)
		length := b.Read32()
		b.ReadPadding(8)
		return &DataKV{inode, second, second + uint64(length), nil}
	case DataKVDataSig:
		b.ReadPadding(3)
		length := b.Read32()
		data := b.ReadData(length)
		b.ReadPadding(4)
		return &DataKV{inode, second, second + uint64(length), data}
	case DirKVSig:
		filenamelen := b.Read8()
		mode := b.Read8()
		b.ReadPadding(1)
		filename := string(b.ReadData(uint32(filenamelen)))
		fileInode := b.Read64()
		return &DirKV{inode, second, filename, fileInode, mode}
	case AttrKVTombstoneSig:
		namelen := b.Read8()
		b.ReadPadding(2)
		name := string(b.ReadData(uint32(namelen)))
		b.ReadPadding(8)
		return &AttrKV{inode, name, nil}
	case AttrKVDataSig:
		namelen := b.Read8()
		length := b.Read16()
		name := string(b.ReadData(uint32(namelen)))
		data := b.ReadData(uint32(length))
		b.ReadPadding(4)
		return &AttrKV{inode, name, data}
	default:
		return nil
	}
}

func DeserializeKey(b *BlockReader) Key {
	inode := b.Read64()
	second := b.Read64()
	switch b.Read8() {
	case DataKeySig:
		b.ReadPadding(7)
		return &DataKey{inode, second}
	case DirKeySig:
		filenamelen := b.Read8()
		b.ReadPadding(2)
		filename := string(b.ReadData(uint32(filenamelen)))
		return &DirKey{inode, second, filename}
	case AttrKeySig:
		// TODO assert second == 0
		namelen := b.Read8()
		b.ReadPadding(2)
		name := string(b.ReadData(uint32(namelen)))
		return &AttrKey{inode, name}
	default:
		return nil
	}
}
