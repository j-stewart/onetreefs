# One Tree FS

**Experimental**. Do not store data you care about in this
filesystem. See [Status](#status-and-future-work) for more
information.

One Tree FS is a FUSE filesystem written in Go, backed by a
[Bε-tree](https://www3.cs.stonybrook.edu/~bender/newpub/2015-BenderFaJa-login-wods.pdf). A
Bε-tree is a type of B-tree where every intermediate node keeps a log
of writes. This general approach combines most of the ease of writes
in a log-based filesystem with most of the lookup performance of a
B-tree based filesystem.

The advantage of this approach is that multiple small writes even to
disparate files and metadata can be coalesced into a single larger
disk write, theoretically increasing performance. As writes propagate
down the tree files will naturally get defragmented.

One Tree FS is loosely inspired by [gefs](https://orib.dev/gefs.html),
a Bε-tree based filesystem for Plan9. The key differences are that One
Tree FS stores all data blocks in its 4 MiB tree nodes whereas gefs
has 16 KiB for both tree nodes and data blocks. The tree is
copy-on-write and in principle supports snapshots.

Another Bε-tree based filesystem is
[BetrFS](https://www.betrfs.org/). The paper reports very good
performance.

## Design

As implied in the name, all data and most metadata are stored in a
single Bε-tree. This trades complexity of the tree implementation for
a single unified store and better locality of data. There is an
additional _root log_ which stores a pointer to the current root of
the tree as well as free block and snapshot information.

The primary key for all data is a file's inode number. There are three
types of secondary keys: attribute keys, directory keys, and data
keys. All inodes will have attribute keys whereas only directories
have directory keys and other files have data keys.

Attribute keys have an attribute name in addition to the inode
number. The value is either a tombstone (for logged deletions) or a
data payload. Directory keys have a filename hash and a filename
string in addition to the inode. The value is the target inode of the
directory entry as well as the file's mode. Data keys have a start and
end offset and either a tombstone payload or a data payload as the
value. Data keys never span a 4KiB page boundary so larger data values
are split chunks of at most 4KiB on insertions.

The Bε-tree supports one mutation operation, Upsert, and three read
operations: Get, GetNext, and GetData. Upsert inserts a list of
key-value pairs into the tree, some of which may be tombstones
representing deletions. When a tombstone reaches an existing data
value during log compaction the data value is removed and the
tombstone remains, except at the leaves where the tombstone is no
longer needed as it cannot be flushed any further.

Get returns a value for a specific key. GetNext returns the successor
to a key if one exists. GetData fetches data values over a range to
efficiently implement data reads from the tree. In practice, Get is
used mostly for directory and attribute lookup, GetNext is used for
directory listing, and GetData is used for file reads.

```
                 +-----------------------------------------+
                 |child ptrs | log | log | log ...         |
                 +-----------------------------------------+
                      /                 |                 \
                    /                   |                  \
  +----------------------+   +----------------------+   +---------------------+
  |data | log | log...   |   |data | log | log...   |   |data | log | log...  |
  +----------------------+   +----------------------+   +---------------------+
```

A depth-2 tree with one branch node at the root and three leaf children.

The tree has two kinds of node: leaves and branches, both up to 4 MiB
in size. Leaves in the tree are structured as a log. Upserts are
appended to the leaf. When a leaf overflows the log is compacted and
if the result is still too large the leaf is split. Branches consist
of child pointers as in a regular B-Tree and a log where upserts are
appended. When the log overflows it is compacted. If the compacted log
is not substantially smaller then the entire log is flushed to the
appropriate child nodes. The threshold on compacted branch logs
reduces the number of times a nearly full branch node will be
rewritten. 

Storing data in-tree instead of externally allocating data blocks
separately simplifies the block allocator. There is only one large
size of block to allocate. Data is naturally defragmented as it
reaches the leaves. Small files are stored efficiently, taking up only
the amount of space of their data. In a quick scan of my machine's
filesystem 60% of files are smaller than 4KiB and 90% of files are
smaller than 64 KiB. Small files represent less than 1% of the data
bytes stored on the filesystem on my machine.

The flushing of the entire branch node's log is a divergence from
other implementations of Bε-trees (which flush only to the child which
would remove the most data from the node's log). This incurs more
instantaneous write cost but reduces the number of times a piece of
data will be rewritten at a level of the tree. The standard approach
of flushing only some keys combined with keeping data in-tree would
result in potentially many more rewrites of data blocks in
intermediate logs. In a simulation this leads to fewer writes per data
block.

The root of the tree needs to be stored somewhere. That place is the
rootlog. The rootlog tracks the roots of snapshots (currently there is
exactly one) and allocated/free blocks. As Upserts occur entries are
appended to the rootlog. The rootlog is written in a circular buffer
spanning two btree blocks (so 8 MiB by default). Each time the rootlog
is half full a compacted summary is written to the log.

On crash, data within the tree will always be consistent. The tree is
synced to disk before the rootlog is written. Writing the new root to
the rootlog is when the data is considered committed. All data on disk
is protected by a CRC so incomplete writes will fail a CRC
check. Currently CRC failures cause a crash for enhanced debugging but
it would be straightforward to recover to the last complete tree
write.

Mutations to the tree are serialized via mutexes on the current
snapshot and the rootlog. Reads briefly take a lock to read the
current root of the tree and then proceed concurrently. There is scope
for improvement here as we could safely allow reads to proceed with
stale data during writes. At the FUSE layer there aren't currently
locks. Per-inode locks may be needed for correctness in some cases
during file attribute mutations. It's unclear from the FUSE
documentation how much of this is serialized by the kernel as it would
be in an in-kernel filesystem.

## Use

Create a disk image:

```
go build ./cmd/format && ./format disk.img
```

Creating a disk image will write a superblock and an empty root
log. The file will grow as new tree blocks are written.

To mount the disk image (requires FUSE):

```
go build ./cmd/fs
mkdir mnt
./fs disk.img mnt
```

Fuse for linux is available in most distributions. On Macs, [Fuse for
Mac](https://macfuse.github.io/) should work in theory but is
untested.

To unmount, run `fusermount -u mnt` on Linux or `umount mnt` on Mac.

## Testing

Run `go test`.

## Structure

- `node.go`, `leaf.go`, `branch.go`, `segment.go` - The Bε-tree implementation
- `kv.go` implementation of key-values as stored in the tree
- `block.go`, `dev.go` low level serialization
- `rootlog.go` the rootlog implementation
- `snapshot.go` snapshots
- `super.go` the superblock, and glue between the rootlog, tree and filesystem
- `cmd/fs/rawfs.go` the FUSE layer that turns the tree into a filesystem
- `attr.go` serialization of file attributes (i.e. `struct stat`)

## Status and Future Work

Should you use this for anything real? LOL, no. There are bugs,
rough edges and a few key missing features. While there are tests it
has not been exhaustively tested.

Limitations:

- Neither `RmDir` nor `Rename` are implemented in FUSE.
- Currently files that have been unlinked (deleted) are removed
  immediately when their link count goes to 0 whereas they should be
  kept around until they have been closed by all filehandles.
- The disk image file will grow without bound because no attempt is
  made to enforce a disk size.
- I have some suspicions around bugs in deleting files.

Future Work:

No attempt has been made at optimizing performance. There is a lot of
low-hanging fruit to be plucked. In particular batching writes to the
tree would improve write performance dramatically.

File data is currently stored at one entry per 4k block, but with no
change to the on-disk format and modest coding complexity could move
to extend based allocation for file data.

There is the machinery in place for multiple mutable snapshots to
coexist in the filesystem but that is not exposed externally nor has
it been tested. Within the context of FUSE that would require some
rearchitecting because the design assumes that the root log is only
accessed from a single process and each FUSE mountpoint requires a
separate process.

Extended attributes are supported within the tree but not exposed to
FUSE.

Many symbols in the code are public that don't need to be.

On deletions the tree should be rebalanced to keep O(log n) lookups.

We should recover last known good filesystem state when reading
corrupted data.

## AI Statement

Everything in this repo is human written. I used AI tools to do some
research into particulars of the FUSE API and the implementation of
existing filesystems and to proofread this README.

## License

MIT
