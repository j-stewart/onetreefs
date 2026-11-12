package onetreefs

import (
	"slices"
	"testing"
)

func TestCompaction(t *testing.T) {
	snap := createForTest(testSuperblock)
	nkeys := 100
	for i := range nkeys {
		snap.Upsert([]KV{
			&AttrKV{uint64(i), "test",
				[]byte{byte(i), byte(i * 2), byte(i * 3), byte(i * 5)}},
		})
	}

	for i := range nkeys {
		kv, errno := snap.Get(&AttrKey{uint64(i), "test"})
		if errno != 0 {
			t.Fatal("got error: ", errno)
		}
		if akv, ok := kv.(*AttrKV); ok {
			if !slices.Equal(
				akv.Data,
				[]byte{byte(i), byte(i * 2), byte(i * 3), byte(i * 5)}) {
				t.Error("Bad value: ", akv.Data)
			}
		} else {
			t.Error("Bad kv: ", kv)
		}
	}

	snap.clearCache()

	for i := range nkeys {
		kv, errno := snap.Get(&AttrKey{uint64(i), "test"})
		if errno != 0 {
			t.Fatal("got error: ", errno)
		}
		if akv, ok := kv.(*AttrKV); ok {
			if !slices.Equal(
				akv.Data,
				[]byte{byte(i), byte(i * 2), byte(i * 3), byte(i * 5)}) {
				t.Error("Bad value: ", akv.Data)
			}
		} else {
			t.Error("Bad kv: ", kv)
		}
	}
}

func TestDataRead(t *testing.T) {
	snap := createForTest(testSuperblock)
	nkeys := 3000
	for i := range nkeys {
		snap.Upsert([]KV{
			&DataKV{3, uint64(i), uint64(i) + 1,
				[]byte{byte(i)}},
		})
	}

	dest := make([]byte, nkeys)
	data, errno := snap.GetData(3, 0, dest)
	if errno != 0 {
		t.Fatal("got error: ", errno)
	}
	for i := range nkeys {
		if data[i] != byte(i) {
			t.Errorf("offset %d got %d expected %d", i, data[i], byte(i))
		}
	}

	snap.clearCache()

	dest = make([]byte, nkeys)
	data, errno = snap.GetData(3, 0, dest)
	if errno != 0 {
		t.Fatal("got error: ", errno)
	}
	for i := range nkeys {
		if data[i] != byte(i) {
			t.Errorf("offset %d got %d expected %d", i, data[i], byte(i))
		}
	}
}

func makeBytes(offset, length int) []byte {
	result := make([]byte, length)
	for i := range length {
		result[i] = byte((i + offset) * i)
	}
	return result
}

func TestTombstone(t *testing.T) {
	snap := createForTest(testSuperblock)
	// write an attr, a dir, and some data
	snap.Upsert([]KV{
		&AttrKV{4, "", []byte{4, 5, 6}},
		&DirKV{4, HashFilename("foo"), "foo", 7, 0},
		&DataKV{4, 0, 7, makeBytes(0, 7)},
	})
	// write enough to push those down to a leaf
	kvs := make([]KV, 30)
	for i := range 30 {
		kvs[i] = &DataKV{5, uint64(4096 * i), uint64(4096 * (i + 1)), makeBytes(4096*i, 4096)}
	}
	snap.Upsert(kvs)

	// read and make sure they're there
	kv, errno := snap.Get(&AttrKey{4, ""})
	if kv == nil || errno != 0 {
		t.Fatalf("missing attrkey (errno %v)\n", errno)
	}
	if !slices.Equal(kv.(*AttrKV).Data, []byte{4, 5, 6}) {
		t.Errorf("incorrect attr data. Expected %v got %v\n", []byte{4, 5, 6}, kv.(*AttrKV).Data)
	}
	kv, errno = snap.Get(&DirKey{4, HashFilename("foo"), "foo"})
	if kv == nil || errno != 0 {
		t.Fatalf("missing dirkey (errno %v)\n", errno)
	}
	if kv.(*DirKV).FileInode != 7 {
		t.Errorf("incorrect dir entry fileinode. Expected 7 got %d\n", kv.(*DirKV).FileInode)
	}
	dest := make([]byte, 7)
	result, errno := snap.GetData(4, 0, dest)
	if errno != 0 {
		t.Fatalf("missing data (errno %v)\n", errno)
	}
	if !slices.Equal(result, makeBytes(0, 7)) {
		t.Errorf("incorrect data entry. expected %v got %v\n", makeBytes(0, 7), result)
	}

	// write tombstones for each
	snap.Upsert([]KV{
		&AttrKV{4, "", nil},
		&DirKV{4, HashFilename("foo"), "foo", 0, 0},
		&DataKV{4, 0, 7, nil},
	})
	// read and make sure they don't come back

	snap.clearCache()

	kv, errno = snap.Get(&AttrKey{4, ""})
	if kv != nil || errno != 0 {
		t.Errorf("failure looking up deleted attr. errno %v value %v\n", errno, kv)
	}
	kv, errno = snap.Get(&DirKey{4, HashFilename("foo"), "foo"})
	if kv != nil || errno != 0 {
		t.Errorf("failure looking up deleted dir. errno %v value %v\n", errno, kv)
	}
	result, errno = snap.GetData(4, 0, dest)
	if !slices.Equal(result, make([]byte, 7)) || errno != 0 {
		t.Errorf("failure looking up deleted data. errno %v value %v\n", errno, kv)
	}
}
