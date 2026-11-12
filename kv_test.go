package onetreefs

import (
	"slices"
	"testing"
)

/*
Tests:
   - roundtrip serialize
*/

func FuzzTombstones(f *testing.F) {
	f.Add(uint64(0), uint64(0))
	f.Add(uint64(0), uint64(4096))
	f.Add(uint64(1), uint64(4095))
	f.Add(uint64(1024), uint64(7500))
	f.Add(uint64(9723), uint64(432917))
	var pagesize uint64 = 4096
	f.Fuzz(func(t *testing.T, start, end uint64) {
		if end < start {
			t.SkipNow()
		}
		kvs := dataTombstones(uint32(pagesize), 111, start, end)
		for _, kv := range kvs {
			tkv := kv.(*DataKV)
			if tkv.Inode != 111 {
				t.Errorf("bad inode: %d", tkv.Inode)
			}
			if tkv.Data != nil {
				t.Errorf("unexpected data of length %d", len(tkv.Data))
			}
		}
		if len(kvs) == 0 {
			if start != end {
				t.Errorf("input start(%d) != end(%d) but no output", start, end)
			}
		} else if len(kvs) == 1 {
			tkv := kvs[0].(*DataKV)
			if tkv.Start != start {
				t.Errorf("input start %d != output start %d", start, tkv.Start)
			}
			if tkv.End != end {
				t.Errorf("input end %d != output end %d", end, tkv.End)
			}
			if start/pagesize != (end-1)/pagesize {
				t.Errorf("Got 1 tombstone (%d, %d) but input spanned a page %d-%d",
					tkv.Start, tkv.End, start, end)
			}
		} else {
			first := kvs[0].(*DataKV)
			last := kvs[len(kvs)-1].(*DataKV)
			if first.Start != start {
				t.Errorf("input start %d != output start %d", start, first.Start)
			}
			if last.End != end {
				t.Errorf("input end %d != output end %d", end, last.End)
			}
			for i := 1; i < len(kvs)-1; i++ {
				tkv := kvs[i].(*DataKV)
				prev := kvs[i-1].(*DataKV)
				if tkv.Start%pagesize != 0 {
					t.Errorf("misaligned kv start %d", tkv.Start)
				}
				if tkv.End%pagesize != 0 {
					t.Errorf("misaligned kv start %d", tkv.End)
				}
				if tkv.End-tkv.Start != pagesize {
					t.Errorf("unexpected kv length %d", tkv.End-tkv.Start)
				}
				if prev.End != tkv.Start {
					t.Errorf("gap between kvs prev.End=%d this.Start=%d", prev.End, tkv.Start)
				}
			}
			prev := kvs[len(kvs)-2].(*DataKV)
			if prev.End != last.Start {
				t.Errorf("gap between kvs prev.End=%d this.Start=%d", prev.End, last.Start)
			}
		}
	})
}

func lotsabytes(seed, start, end uint64) []byte {
	if end-start > 4*1024*1024 {
		return nil
	}
	b := make([]byte, end-start)
	for i := range b {
		n := uint64(i) + start
		b[i] = byte(((n + seed) * n) % 256)
	}
	return b
}

func FuzzBytesToKVs(f *testing.F) {
	f.Add(uint64(0), uint64(0))
	f.Add(uint64(0), uint64(4096))
	f.Add(uint64(1), uint64(4095))
	f.Add(uint64(1024), uint64(7500))
	f.Add(uint64(9723), uint64(432917))
	var pagesize uint64 = 4096
	f.Fuzz(func(t *testing.T, start, end uint64) {
		data := lotsabytes(37, start, end)
		if data == nil {
			t.Skip()
		}
		kvs := bytesToKVs(uint32(pagesize), 111, start, data)
		for _, kv := range kvs {
			tkv := kv.(*DataKV)
			if tkv.Inode != 111 {
				t.Errorf("bad inode: %d", tkv.Inode)
			}
			if len(tkv.Data) != int(tkv.End-tkv.Start) {
				t.Errorf("unexpected data of length %d", len(tkv.Data))
			}
		}
		if len(kvs) == 0 {
			if len(data) != 0 {
				t.Errorf("input length %d but no output", len(data))
			}
		} else if len(kvs) == 1 {
			tkv := kvs[0].(*DataKV)
			if tkv.Start != start {
				t.Errorf("input start %d != output start %d", start, tkv.Start)
			}
			if tkv.End != end {
				t.Errorf("input end %d != output end %d", end, tkv.End)
			}
			if start/pagesize != (end-1)/pagesize {
				t.Errorf("Got 1 tombstone (%d, %d) but input spanned a page %d-%d",
					tkv.Start, tkv.End, start, end)
			}
			if !slices.Equal(tkv.Data, data) {
				t.Error("bad data")
			}
		} else {
			first := kvs[0].(*DataKV)
			last := kvs[len(kvs)-1].(*DataKV)
			if first.Start != start {
				t.Errorf("input start %d != output start %d", start, first.Start)
			}
			if last.End != end {
				t.Errorf("input end %d != output end %d", end, last.End)
			}
			offset := first.End - first.Start
			if !slices.Equal(first.Data, data[:offset]) {
				t.Errorf("bad data")
			}
			for i := 1; i < len(kvs)-1; i++ {
				tkv := kvs[i].(*DataKV)
				prev := kvs[i-1].(*DataKV)
				if tkv.Start%pagesize != 0 {
					t.Errorf("misaligned kv start %d", tkv.Start)
				}
				if tkv.End%pagesize != 0 {
					t.Errorf("misaligned kv start %d", tkv.End)
				}
				if tkv.End-tkv.Start != pagesize {
					t.Errorf("unexpected kv length %d", tkv.End-tkv.Start)
				}
				if prev.End != tkv.Start {
					t.Errorf("gap between kvs prev.End=%d this.Start=%d", prev.End, tkv.Start)
				}
				offsetNext := offset + tkv.End - tkv.Start
				if !slices.Equal(tkv.Data, data[offset:offsetNext]) {
					t.Errorf("bad data")
				}
				offset = offsetNext
			}
			prev := kvs[len(kvs)-2].(*DataKV)
			if prev.End != last.Start {
				t.Errorf("gap between kvs prev.End=%d this.Start=%d", prev.End, last.Start)
			}
			if !slices.Equal(last.Data, data[offset:]) {
				t.Errorf("bad data")
			}
		}
	})
}

func TestMergeDifferentKindKVs(t *testing.T) {
	a := AttrKV{5, "hello", nil}
	b := DataKV{5, 1024, 2048, nil}
	c := DirKV{5, HashFilename("world"), "world", 127, 0}

	ab := a.Merge(&b)
	ac := a.Merge(&c)
	bc := b.Merge(&c)
	ba := b.Merge(&a)
	ca := c.Merge(&a)
	cb := c.Merge(&b)

	if ab[0] != &a || ab[1] != &b {
		t.Fail()
	}
	if ac[0] != &a || ac[1] != &c {
		t.Fail()
	}
	if bc[0] != &b || bc[1] != &c {
		t.Fail()
	}
	if ba[0] != &a || ba[1] != &b {
		t.Fail()
	}
	if ca[0] != &a || ca[1] != &c {
		t.Fail()
	}
	if cb[0] != &b || cb[1] != &c {
		t.Fail()
	}
}

func TestMergeDifferentInodeKVs(t *testing.T) {
	a := AttrKV{5, "hello", nil}
	b := AttrKV{4, "goodbye", nil}

	ab := a.Merge(&b)

	if ab[0] != &b || ab[1] != &a {
		t.Fail()
	}
}

func TestMergeDifferentNameKVs(t *testing.T) {
	a := AttrKV{4, "hello", nil}
	b := AttrKV{4, "goodbye", nil}

	ab := a.Merge(&b)

	if ab[0] != &b || ab[1] != &a {
		t.Fail()
	}
}

func TestMergeSameNameAttrKVs(t *testing.T) {
	a := AttrKV{4, "hello", []byte{4, 5}}
	b := AttrKV{4, "hello", []byte{6, 7}}

	ab := a.Merge(&b)

	if ab[0] != &b || len(ab) != 1 {
		t.Fail()
	}
}

func TestMergeSameNameDirKVs(t *testing.T) {
	a := DirKV{5, HashFilename("world"), "world", 127, 0}
	b := DirKV{5, HashFilename("world"), "world", 11, 0}

	ab := a.Merge(&b)

	if ab[0] != &b || len(ab) != 1 {
		t.Fail()
	}
}

func FuzzMergeDataTombstoneKVs(f *testing.F) {
	f.Add(uint64(0), uint64(0), uint64(100), uint64(100))
	f.Add(uint64(0), uint64(10), uint64(100), uint64(100))
	f.Add(uint64(0), uint64(10), uint64(90), uint64(100))
	f.Add(uint64(0), uint64(10), uint64(100), uint64(90))
	f.Add(uint64(10), uint64(0), uint64(100), uint64(90))
	f.Fuzz(func(t *testing.T, a0, b0, a1, b1 uint64) {
		if a0 > a1 {
			a0, a1 = a1, a0
		}
		if b0 > b1 {
			b0, b1 = b1, b0
		}
		a := DataKV{5, a0, a1, nil}
		b := DataKV{5, b0, b1, nil}

		if !a.Overlaps(&b) {
			t.Skip()
		}

		ab := a.Merge(&b)

		if len(ab) != 1 {
			t.Fatal("tombstone data merge should have one result")
		}
		dkv := ab[0].(*DataKV)
		if dkv.Inode != 5 {
			t.Error("bad inode")
		}
		if dkv.Start != min(a0, b0) || dkv.End != max(a1, b1) {
			t.Errorf("bad bounds; expected (%d,%d) got (%d,%d)",
				min(a0, b0), max(a1, b1), dkv.Start, dkv.End)
		}
	})
}

func FuzzMergeDataKVs(f *testing.F) {
	f.Add(uint64(0), uint64(0), uint64(100), uint64(100))
	f.Add(uint64(0), uint64(10), uint64(100), uint64(100))
	f.Add(uint64(0), uint64(10), uint64(90), uint64(100))
	f.Add(uint64(0), uint64(10), uint64(100), uint64(90))
	f.Add(uint64(10), uint64(0), uint64(100), uint64(90))
	f.Fuzz(func(t *testing.T, a0, b0, a1, b1 uint64) {
		if a0 > a1 {
			a0, a1 = a1, a0
		}
		if b0 > b1 {
			b0, b1 = b1, b0
		}
		a := DataKV{5, a0, a1, lotsabytes(9, a0, a1)}
		b := DataKV{5, b0, b1, lotsabytes(17, a0, a1)}

		if !a.Overlaps(&b) {
			t.Skip()
		}

		ab := a.Merge(&b)

		if len(ab) != 1 {
			t.Fatal("data merge should have one result")
		}
		dkv := ab[0].(*DataKV)
		if dkv.Inode != 5 {
			t.Error("bad inode")
		}
		if dkv.Start != min(a0, b0) || dkv.End != max(a1, b1) {
			t.Errorf("bad bounds; expected (%d,%d) got (%d,%d)",
				min(a0, b0), max(a1, b1), dkv.Start, dkv.End)
		}
	})
}

func TestCmpKeys(t *testing.T) {
	dataKeyA := &DataKey{1, 42}
	dataKeyB := &DataKey{1, 95}
	dataKeyC := &DataKey{2, 0}
	dirKeyA := &DirKey{1, 6, "x"}
	dirKeyB := &DirKey{1, 11, "y"}
	dirKeyC := &DirKey{2, 5, "z"}
	attrKeyA := &AttrKey{1, "a"}
	attrKeyB := &AttrKey{1, "b"}
	attrKeyC := &AttrKey{2, "a"}

	// within-kind
	if CmpKeys(dataKeyA, dataKeyB) >= 0 {
		t.Fail()
	}
	if CmpKeys(dataKeyB, dataKeyA) <= 0 {
		t.Fail()
	}
	if CmpKeys(dataKeyA, dataKeyC) >= 0 {
		t.Fail()
	}
	if CmpKeys(dataKeyC, dataKeyA) <= 0 {
		t.Fail()
	}
	if CmpKeys(dirKeyA, dirKeyB) >= 0 {
		t.Fail()
	}
	if CmpKeys(dirKeyB, dirKeyA) <= 0 {
		t.Fail()
	}
	if CmpKeys(dirKeyA, dirKeyC) >= 0 {
		t.Fail()
	}
	if CmpKeys(dirKeyC, dirKeyA) <= 0 {
		t.Fail()
	}
	if CmpKeys(attrKeyA, attrKeyB) >= 0 {
		t.Fail()
	}
	if CmpKeys(attrKeyB, attrKeyA) <= 0 {
		t.Fail()
	}
	if CmpKeys(attrKeyA, attrKeyC) >= 0 {
		t.Fail()
	}
	if CmpKeys(attrKeyC, attrKeyA) <= 0 {
		t.Fail()
	}
	// cross-kind
	if CmpKeys(attrKeyA, dataKeyA) >= 0 {
		t.Fail()
	}
	if CmpKeys(dataKeyA, attrKeyA) <= 0 {
		t.Fail()
	}
	if CmpKeys(dataKeyA, dirKeyA) >= 0 {
		t.Fail()
	}
	if CmpKeys(dirKeyA, dataKeyA) <= 0 {
		t.Fail()
	}
	if CmpKeys(attrKeyA, dataKeyA) >= 0 {
		t.Fail()
	}
	if CmpKeys(dataKeyA, attrKeyA) <= 0 {
		t.Fail()
	}
	// cross-inode
	if CmpKeys(dataKeyA, attrKeyC) >= 0 {
		t.Error("cross-inode")
	}
}
