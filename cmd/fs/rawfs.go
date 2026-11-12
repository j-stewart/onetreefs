package main

import (
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"
	"setbang.org/onetreefs"
)

type OneTreeFileSystem struct {
	fuse.RawFileSystem
	snap       *onetreefs.Snapshot
	nextFh     uint64
	dirHandles map[uint64]*onetreefs.DirKV
	lock       sync.Mutex
}

func (fs *OneTreeFileSystem) Lookup(
	cancel <-chan struct{},
	header *fuse.InHeader,
	name string,
	out *fuse.EntryOut) (status fuse.Status) {
	dkv, err := fs.GetDirEnt(header.NodeId, name)
	out.AttrValid = 86400
	out.EntryValid = 86400
	if err != 0 {
		return err
	}
	if dkv == nil {
		return fuse.ENOENT
	}
	out.NodeId = dkv.FileInode
	attr, err := fs.RawGetAttr(dkv.FileInode)
	if err != 0 {
		return err
	}
	out.Attr = *attr
	out.Generation = 0
	return fuse.OK
}

func (fs *OneTreeFileSystem) GetAttr(
	cancel <-chan struct{},
	input *fuse.GetAttrIn,
	out *fuse.AttrOut) (code fuse.Status) {
	attr, err := fs.RawGetAttr(input.NodeId)
	if err != 0 {
		return err
	}
	out.Attr = *attr
	out.AttrValid = 86400
	return fuse.OK
}

func (fs *OneTreeFileSystem) SetAttr(
	cancel <-chan struct{},
	input *fuse.SetAttrIn,
	out *fuse.AttrOut) (code fuse.Status) {
	attr, err := fs.RawGetAttr(input.NodeId)
	var kvs []onetreefs.KV
	if err != 0 {
		return err
	}
	if size, ok := input.GetSize(); ok {
		// truncate the files as needed
		if size < attr.Size {
			kvs = append(kvs,
				onetreefs.DataTombstones(fs.snap, attr.Ino, size, attr.Size)...)
		}
		attr.Size = size
	}
	if mode, ok := input.GetMode(); ok {
		attr.Mode = mode
	}
	if atime, ok := input.GetATime(); ok {
		attr.Atime = uint64(atime.Unix())
		attr.Atimensec = uint32(atime.UnixNano())
	}
	if mtime, ok := input.GetMTime(); ok {
		attr.Mtime = uint64(mtime.Unix())
		attr.Mtimensec = uint32(mtime.UnixNano())
	}
	if ctime, ok := input.GetCTime(); ok {
		attr.Ctime = uint64(ctime.Unix())
		attr.Ctimensec = uint32(ctime.UnixNano())
	}
	if uid, ok := input.GetUID(); ok {
		attr.Owner.Uid = uid
	}
	if gid, ok := input.GetGID(); ok {
		attr.Owner.Gid = gid
	}
	kvs = append(kvs, onetreefs.SerializeAttr(attr))
	fs.snap.Upsert(kvs)
	out.Attr = *attr
	out.AttrValid = 86400
	return fuse.OK
}

func (fs *OneTreeFileSystem) Mknod(
	cancel <-chan struct{},
	input *fuse.MknodIn,
	name string, out *fuse.EntryOut) (code fuse.Status) {
	dkv, err := fs.GetDirEnt(input.NodeId, name)
	if err != 0 {
		return err
	}
	if dkv != nil {
		return fuse.EPERM
	}
	fs.MakeInode(input.Mode, input.Rdev, input.Owner, out)
	fs.snap.Upsert([]onetreefs.KV{
		&onetreefs.DirKV{
			input.NodeId, onetreefs.HashFilename(name), name,
			out.Attr.Ino, byte(out.Attr.Mode >> 12)},
		onetreefs.SerializeAttr(&out.Attr)})
	return fuse.OK
}

func (fs *OneTreeFileSystem) Mkdir(
	cancel <-chan struct{},
	input *fuse.MkdirIn,
	name string, out *fuse.EntryOut) (code fuse.Status) {
	dkv, err := fs.GetDirEnt(input.NodeId, name)
	if err != 0 {
		return err
	}
	if dkv != nil {
		return fuse.EPERM
	}
	attr, err := fs.RawGetAttr(input.NodeId)
	if err != 0 {
		return err
	}
	attr.Nlink++ // for the new directory's .. entry
	fs.MakeInode(input.Mode|syscall.S_IFDIR, 0, input.Owner, out)
	fs.snap.Upsert([]onetreefs.KV{
		onetreefs.SerializeAttr(attr),
		&onetreefs.DirKV{input.NodeId, onetreefs.HashFilename(name), name,
			out.Attr.Ino, byte(out.Attr.Mode >> 12)},
		&onetreefs.DirKV{out.Attr.Ino, onetreefs.HashFilename("."), ".",
			out.Attr.Ino, syscall.DT_DIR},
		&onetreefs.DirKV{out.Attr.Ino, onetreefs.HashFilename(".."), "..",
			input.NodeId, syscall.DT_DIR},
		onetreefs.SerializeAttr(&out.Attr)})
	return fuse.OK
}

func (fs *OneTreeFileSystem) Unlink(
	cancel <-chan struct{},
	header *fuse.InHeader,
	name string) (code fuse.Status) {
	dkv, err := fs.GetDirEnt(header.NodeId, name)
	if err != 0 {
		return err
	}
	attr, err := fs.RawGetAttr(dkv.FileInode)
	if err != 0 {
		return err
	}
	attr.Nlink--
	var update []onetreefs.KV
	// TODO we might have reference in memory so may not be able to delete
	if attr.Nlink == 0 {
		// tombstones for the data
		update = append(update,
			onetreefs.DataTombstones(fs.snap, dkv.FileInode, 0, attr.Size)...)
		// create tombstone for the stat
		update = append(update, &onetreefs.AttrKV{dkv.FileInode, "", nil})
	} else {
		update = append(update, onetreefs.SerializeAttr(attr))
	}
	update = append(update,
		&onetreefs.DirKV{dkv.Inode, dkv.Hash, dkv.Filename, 0, dkv.Mode})
	fs.snap.Upsert(update)
	return fuse.OK
}

// Rmdir
// Rename

func (fs *OneTreeFileSystem) Link(
	cancel <-chan struct{},
	input *fuse.LinkIn,
	name string, out *fuse.EntryOut) (code fuse.Status) {
	dkv, err := fs.GetDirEnt(input.NodeId, name)
	if err != 0 {
		return err
	}
	if dkv != nil {
		return fuse.EPERM
	}
	attr, err := fs.RawGetAttr(input.Oldnodeid)
	out.Attr = *attr
	fs.snap.Upsert([]onetreefs.KV{
		&onetreefs.DirKV{input.NodeId, onetreefs.HashFilename(name), name,
			out.Attr.Ino, byte(out.Attr.Mode >> 12)},
		onetreefs.SerializeAttr(&out.Attr)})
	return fuse.OK
}

func (fs *OneTreeFileSystem) Symlink(
	cancel <-chan struct{},
	header *fuse.InHeader,
	pointedTo string,
	linkName string,
	out *fuse.EntryOut) (code fuse.Status) {
	dkv, err := fs.GetDirEnt(header.NodeId, linkName)
	if err != 0 {
		return err
	}
	if dkv != nil {
		return fuse.EPERM
	}
	fs.MakeInode(syscall.S_IFLNK|0777, 0, header.Owner, out)
	out.Attr.Size = uint64(len(pointedTo))
	fs.snap.Upsert([]onetreefs.KV{
		&onetreefs.DirKV{header.NodeId, onetreefs.HashFilename(linkName), linkName,
			out.Attr.Ino, byte(out.Attr.Mode >> 12)},
		onetreefs.SerializeAttr(&out.Attr),
		&onetreefs.DataKV{out.Attr.Ino, 0, out.Attr.Size, []byte(pointedTo)}})
	return fuse.OK
}

const MAX_SYMLINK = 4096

func (fs *OneTreeFileSystem) Readlink(
	cancel <-chan struct{},
	header *fuse.InHeader) (out []byte, code fuse.Status) {
	contents := make([]byte, 0, MAX_SYMLINK)
	out, errno := fs.snap.GetData(header.NodeId, 0, contents)
	return out, errnoToStatus(errno)
}

// Access
// misc Xattr stuff

func (fs *OneTreeFileSystem) Create(
	cancel <-chan struct{},
	input *fuse.CreateIn,
	name string,
	out *fuse.CreateOut) (code fuse.Status) {
	dkv, err := fs.GetDirEnt(input.NodeId, name)
	if err != 0 {
		return err
	}
	if dkv != nil {
		return fuse.EPERM
	}
	fs.MakeInode(input.Mode, 0, input.Owner, &out.EntryOut)
	fs.snap.Upsert([]onetreefs.KV{
		&onetreefs.DirKV{input.NodeId, onetreefs.HashFilename(name), name,
			out.Attr.Ino, byte(out.Attr.Mode >> 12)},
		onetreefs.SerializeAttr(&out.Attr)})
	return fuse.OK
}

func (fs *OneTreeFileSystem) Tmpfile(
	cancel <-chan struct{},
	input *fuse.CreateIn, out *fuse.CreateOut) (code fuse.Status) {
	fs.MakeInode(input.Mode, 0, input.Owner, &out.EntryOut)
	fs.snap.Upsert([]onetreefs.KV{onetreefs.SerializeAttr(&out.Attr)})
	// TODO maybe not complete
	return fuse.OK
}

func (fs *OneTreeFileSystem) Open(
	cancel <-chan struct{},
	input *fuse.OpenIn, out *fuse.OpenOut) (status fuse.Status) {
	// for now a no-op
	return fuse.OK
}

func (fs *OneTreeFileSystem) Read(
	cancel <-chan struct{},
	input *fuse.ReadIn, buf []byte) (fuse.ReadResult, fuse.Status) {
	data, err := fs.snap.GetData(input.NodeId, uint64(input.Offset), buf)
	return fuse.ReadResultData(data), errnoToStatus(err)
}

func (fs *OneTreeFileSystem) Write(
	cancel <-chan struct{},
	input *fuse.WriteIn, data []byte) (written uint32, code fuse.Status) {
	attr, err := fs.RawGetAttr(input.NodeId)
	if err != 0 {
		return 0, err
	}
	kvs := onetreefs.BytesToKVs(fs.snap, input.NodeId, uint64(input.Offset), data)
	if attr.Size < uint64(input.Offset)+uint64(input.Size) {
		attr.Size = uint64(input.Offset) + uint64(input.Size)
		kvs = append(kvs, onetreefs.SerializeAttr(attr))
	}
	fs.snap.Upsert(kvs)
	return uint32(len(data)), fuse.OK
}

func (fs *OneTreeFileSystem) OpenDir(
	cancel <-chan struct{},
	input *fuse.OpenIn,
	out *fuse.OpenOut) (status fuse.Status) {
	fs.lock.Lock()
	defer fs.lock.Unlock()
	fh := fs.nextFh
	fs.nextFh++
	fs.dirHandles[fh] = nil
	out.Fh = fh
	return fuse.OK
}

func (fs *OneTreeFileSystem) ReadDir(
	cancel <-chan struct{},
	input *fuse.ReadIn,
	out *fuse.DirEntryList) fuse.Status {
	fs.lock.Lock()
	handleKV := fs.dirHandles[input.Fh]
	fs.lock.Unlock()
	var key onetreefs.Key
	if input.Offset == 0 {
		// (re-)start; nil handleKV is allowed
		key = &onetreefs.DirKey{input.NodeId, input.Offset, ""}
	} else if handleKV == nil {
		// no longer open
		return fuse.OK
	} else if input.Offset != handleKV.Hash {
		// seek
		key = &onetreefs.DirKey{input.NodeId, input.Offset, ""}
	} else {
		key = handleKV.Key()
	}

	kv, err := fs.snap.GetNext(key)
	if err != 0 {
		return errnoToStatus(err)
	}
	for kv != nil {
		if dkv, ok := kv.(*onetreefs.DirKV); ok {
			if !out.AddDirEntry(fuse.DirEntry{
				Mode: uint32(dkv.Mode) << 12,
				Name: dkv.Filename,
				Ino:  dkv.FileInode,
				Off:  dkv.Hash}) {
				// the DirEntryList is full
				return fuse.OK
			}
			fs.lock.Lock()
			fs.dirHandles[input.Fh] = dkv
			fs.lock.Unlock()
		} else {
			// we've read all the directory entries
			return fuse.OK
		}
		kv, err = fs.snap.GetNext(kv.Key())
		if err != 0 {
			return errnoToStatus(err)
		}
	}
	return fuse.OK
}

func (fs *OneTreeFileSystem) ReleaseDir(input *fuse.ReleaseIn) {
	fs.lock.Lock()
	defer fs.lock.Unlock()
	delete(fs.dirHandles, input.Fh)
}

// ReadDirPlus

func (fs *OneTreeFileSystem) StatFs(
	cancel <-chan struct{},
	input *fuse.InHeader, out *fuse.StatfsOut) (code fuse.Status) {
	sb := fs.snap.Superblock()
	blocks := sb.FsSize - uint64(sb.RootlogRes/sb.BlockSize)
	// everywhere statfs says blocks we mean internal pages
	block2page := uint64(sb.BlockSize / sb.PageSize)
	out.Blocks = blocks * block2page              // total blocks
	out.Bfree = fs.snap.FreeBlocks() * block2page // free blocks
	// TODO save a margin for root
	out.Bavail = out.Bfree   // free for a non-superuser
	out.Files = 0            // total number of inodes
	out.Ffree = 0            // free inodes
	out.Bsize = sb.PageSize  // block size
	out.NameLen = 255        // maximum filename length
	out.Frsize = sb.PageSize // fragment size
	return fuse.OK
}

func (fs *OneTreeFileSystem) GetDirEnt(parentInode uint64, name string) (*onetreefs.DirKV, fuse.Status) {
	kv, err := fs.snap.Get(&onetreefs.DirKey{parentInode, onetreefs.HashFilename(name), name})
	if err != 0 {
		return nil, errnoToStatus(err)
	}
	if kv == nil {
		return nil, fuse.OK
	}
	return kv.(*onetreefs.DirKV), fuse.OK
}

func (fs *OneTreeFileSystem) RawGetAttr(ino uint64) (*fuse.Attr, fuse.Status) {
	kv, err := fs.snap.Get(&onetreefs.AttrKey{ino, ""})
	if kv == nil {
		return nil, fuse.ENOENT
	}
	return onetreefs.DeserializeAttr(kv), errnoToStatus(err)
}

func (fs *OneTreeFileSystem) MakeInode(
	mode uint32,
	rdev uint32,
	owner fuse.Owner,
	out *fuse.EntryOut) {
	out.NodeId = fs.snap.AllocInode()
	out.Attr.Ino = out.NodeId
	out.Attr.Mode = mode
	out.Attr.Nlink = 1
	out.Attr.Owner = owner
	out.Attr.Rdev = rdev
	out.Attr.Size = 0
	// TODO set validity to some long time
	now := time.Now()
	out.Attr.SetTimes(&now, &now, &now)
	out.EntryValid = 86400 // a day
	out.AttrValid = 86400
}

func errnoToStatus(errno syscall.Errno) fuse.Status {
	return fuse.Status(errno)
}
