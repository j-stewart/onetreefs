package main

import (
	"log"
	"os"

	"github.com/hanwen/go-fuse/v2/fuse"
	"setbang.org/onetreefs"
)

func main() {
	diskFile := os.Args[1]
	mountPoint := os.Args[2]
	dev, err := onetreefs.OpenFileDevice(diskFile)
	if err != nil {
		log.Fatalf("error opening disk file: %v\n", err)
	}
	super, errno := onetreefs.ReadSuperRoot(dev)
	if errno != 0 {
		log.Fatalf("error reading superblock: %v\n", errno)
	}
	snap := super.GetSnapshotByName("")
	if snap == nil {
		log.Fatalf("No default snapshot found\n")
	}
	server, err := fuse.NewServer(
		&OneTreeFileSystem{
			RawFileSystem: fuse.NewDefaultRawFileSystem(),
			nextFh:        1,
			dirHandles:    make(map[uint64]*onetreefs.DirKV),
			snap:          snap},
		mountPoint,
		&fuse.MountOptions{
			Debug: true,
			DisableReadDirPlus: true,
			// I think Mac might require this:
			// AllowOther: true,
		})
	if err != nil {
		log.Fatalf("error starting fuse server: %v\n", err)
	}
	server.Serve()
}
