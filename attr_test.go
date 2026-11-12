package onetreefs

import (
	"testing"

	"github.com/hanwen/go-fuse/v2/fuse"
)

func TestAttrRoundTrip(t *testing.T) {
	a := fuse.Attr{
		Ino:       6,
		Size:      7,
		Blocks:    1,
		Blksize:   512,
		Atime:     9,
		Mtime:     10,
		Ctime:     11,
		Atimensec: 12,
		Mtimensec: 13,
		Ctimensec: 14,
		Mode:      15,
		Nlink:     16,
		Uid:       17,
		Gid:       18,
	}

	kv := SerializeAttr(&a)
	out := DeserializeAttr(kv)
	if a != *out {
		t.Logf(" in: %v", &a)
		t.Logf("out: %v", out)
		t.Fail()
	}
}
