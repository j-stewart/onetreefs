package main

import (
	"log"
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"
	"setbang.org/onetreefs"
)

func Format(diskfile string) error {
	log.Printf("formatting %s", diskfile)
	dev, err := onetreefs.CreateFileDevice(diskfile)
	if err != nil {
		return err
	}
	var rootIno uint64 = 1
	ctime := uint64(time.Now().Unix())
	rootAttr := fuse.Attr{
		Ino:   rootIno,
		Size:  4096, // arbitrary
		Mode:  syscall.S_IFDIR | 0755,
		Nlink: 2,
		Atime: ctime,
		Mtime: ctime,
		Ctime: ctime,
		Uid: uint32(os.Getuid()),
		Gid: uint32(os.Getgid()),
	}
	sb := onetreefs.Superblock{
		Version:    1,
		BlockSize:  1 << 22,
		SegSize:    1 << 16,
		PageSize:   1 << 12,
		MaxPivot:   256,
		RootlogRes: 1 << 23,
		FsSize:     1 << 12,
	}
	kvs := []onetreefs.KV{
		onetreefs.SerializeAttr(&rootAttr),
		&onetreefs.DirKV{
			Inode:     rootIno,
			Hash:      onetreefs.HashFilename("."),
			Filename:  ".",
			FileInode: rootIno,
			Mode:      syscall.DT_DIR},
		&onetreefs.DirKV{
			Inode:     rootIno,
			Hash:      onetreefs.HashFilename(".."),
			Filename:  "..",
			FileInode: rootIno,
			Mode:      syscall.DT_DIR},
	}
	slices.SortFunc(kvs, func(a, b onetreefs.KV) int { return onetreefs.CmpKeys(a.Key(), b.Key()) })
	return onetreefs.FormatDevice(dev, sb, kvs)
}

func main() {
	err := Format(os.Args[1])
	if err != nil {
		log.Fatalf("error: %v\n", err)
	}
}
