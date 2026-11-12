package onetreefs

import "github.com/hanwen/go-fuse/v2/fuse"

const attrSize = 64

func SerializeAttr(a *fuse.Attr) KV {
	b := NewSimpleWriter(attrSize)
	b.Write64(a.Size)
	b.Write64(a.Atime)
	b.Write64(a.Mtime)
	b.Write64(a.Ctime)
	b.Write32(a.Atimensec)
	b.Write32(a.Mtimensec)
	b.Write32(a.Ctimensec)
	b.Write32(a.Mode)
	b.Write32(a.Nlink)
	b.Write32(a.Owner.Uid)
	b.Write32(a.Owner.Gid)
	return &AttrKV{a.Ino, "", b.Data()}
}

func DeserializeAttr(kv KV) *fuse.Attr {
	akv := kv.(*AttrKV)
	b := NewBlockReader(akv.Data)
	size := b.Read64()
	var blksize uint32 = 512
	blocks := size / uint64(blksize)
	if size%uint64(blksize) != 0 {
		blocks++
	}
	return &fuse.Attr{
		Ino:       akv.Inode,
		Size:      size,
		Blocks:    blocks,
		Blksize:   blksize,
		Atime:     b.Read64(),
		Mtime:     b.Read64(),
		Ctime:     b.Read64(),
		Atimensec: b.Read32(),
		Mtimensec: b.Read32(),
		Ctimensec: b.Read32(),
		Mode:      b.Read32(),
		Nlink:     b.Read32(),
		Uid:       b.Read32(),
		Gid:       b.Read32(),
	}
}
