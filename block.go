package onetreefs

import (
	"encoding/binary"
	"hash/crc64"
	"log"
)

var table *crc64.Table = crc64.MakeTable(crc64.ECMA)

type BlockRef struct {
	blockno  uint64
	birth    uint32
	children uint32
	size     uint32
}

type Reader interface {
	Read8() uint8
	Read16() uint16
	Read32() uint32
	Read64() uint64
	ReadPadding(int)
	Tell() int
}

type Writer interface {
	Write8(uint8)
	Write16(uint16)
	Write32(uint32)
	Write64(uint64)
	WritePadding(int)
}

type SimpleWriter interface {
	Writer
	Data() []byte
}

type DataWriter interface {
	Writer
	WriteData(data []byte)
}

type BlockWriter struct {
	BlockAllocator
	data []byte
}

type BlockReader struct {
	data   []byte
	offset int
}

type BlockKV struct {
	key   Key
	block BlockRef
}

type BlockAllocator struct {
	*Superblock
	prefix uint32
	smalls uint32
	larges uint32
}

func NewBlockAllocator(sb *Superblock) *BlockAllocator {
	return &BlockAllocator{sb, 16, 0, 0}
}

func NewSimpleWriter(size uint32) SimpleWriter {
	return &BlockWriter{BlockAllocator{nil, 0, 0, 0}, make([]byte, size)}
}

func (b *BlockAllocator) sizes() (uint32, uint32) {
	offset := b.prefix + b.smalls
	if offset%b.PageSize != 0 {
		offset += b.PageSize - (offset % b.PageSize)
	}
	startOfLarges := offset
	offset += b.larges
	return startOfLarges, offset
}

func (b *BlockAllocator) Size() uint32 {
	_, size := b.sizes()
	return size
}

func (l *BlockWriter) Size() uint32 {
	return uint32(len(l.data))
}

func (b *BlockAllocator) ToWriter(sig uint32) *BlockWriter {
	startOfLarges, size := b.sizes()
	data := make([]byte, size)
	binary.LittleEndian.PutUint32(data, sig)
	binary.LittleEndian.PutUint32(data[4:], size-16)
	return &BlockWriter{BlockAllocator{b.Superblock, 16, b.prefix, startOfLarges},
		data}
}

func (l *BlockWriter) Finish() {
	binary.LittleEndian.PutUint64(l.data[8:], crc64.Checksum(l.data[16:], table))
}

func (b *BlockAllocator) Write8(v uint8) {
	b.prefix++
}

func (b *BlockAllocator) Write16(v uint16) {
	b.prefix += 2
}

func (b *BlockAllocator) Write32(v uint32) {
	b.prefix += 4
}

func (b *BlockAllocator) Write64(v uint64) {
	b.prefix += 8
}

func (b *BlockAllocator) WriteData(data []byte) {
	var offset uint32
	datalen := uint32(len(data))
	if datalen > b.PageSize {
		offset = b.larges
		datalen := uint32(len(data))
		if datalen%b.PageSize != 0 {
			datalen += b.PageSize - (datalen % b.PageSize)
		}
		b.larges += datalen
	} else {
		offset = b.smalls
		b.smalls += datalen
	}
	b.Write32(offset)
}

func (b *BlockAllocator) WritePadding(length int) {
	b.prefix += uint32(length)
}

func (b *BlockWriter) Write8(v uint8) {
	b.data[b.prefix] = v
	b.prefix++
}

func (b *BlockWriter) Write16(v uint16) {
	binary.LittleEndian.PutUint16(b.data[b.prefix:], v)
	b.prefix += 2
}

func (b *BlockWriter) Write32(v uint32) {
	binary.LittleEndian.PutUint32(b.data[b.prefix:], v)
	b.prefix += 4
}

func (b *BlockWriter) Write64(v uint64) {
	binary.LittleEndian.PutUint64(b.data[b.prefix:], v)
	b.prefix += 8
}

func (b *BlockWriter) WriteData(data []byte) {
	var offset uint32
	datalen := uint32(len(data))
	if datalen > b.PageSize {
		offset = b.larges
		datalen := uint32(len(data))
		if datalen%b.PageSize != 0 {
			datalen += b.PageSize - (datalen % b.PageSize)
		}
		b.larges += datalen
	} else {
		// TODO 8-byte align small data
		offset = b.smalls
		b.smalls += datalen
	}
	copy(b.data[offset:], data)
	b.Write32(offset)
}

func (b *BlockWriter) WritePadding(length int) {
	b.prefix += uint32(length)
}

func (b *BlockWriter) Data() []byte {
	return b.data
}

func NewBlockReader(data []byte) BlockReader {
	return BlockReader{data, 0}
}

func (b *BlockReader) Tell() int {
	return b.offset
}

func (b *BlockReader) Hash() uint64 {
	return crc64.Checksum(b.data, table)
}

func (b *BlockReader) Read8() (v uint8) {
	v = b.data[b.offset]
	b.offset++
	return
}

func (b *BlockReader) Read16() (v uint16) {
	v = binary.LittleEndian.Uint16(b.data[b.offset:])
	b.offset += 2
	return
}

func (b *BlockReader) Read32() (v uint32) {
	v = binary.LittleEndian.Uint32(b.data[b.offset:])
	b.offset += 4
	return
}

func (b *BlockReader) Read64() (v uint64) {
	v = binary.LittleEndian.Uint64(b.data[b.offset:])
	b.offset += 8
	return
}

func (b *BlockReader) ReadData(length uint32) []byte {
	offset := b.Read32()
	if length == 0 {
		// offset should also be 0
		// we don't return nil because sometimes we distinguish between not present and empty
		return []byte{}
	}
	return b.data[offset : offset+length]
}

func (b *BlockReader) ReadPadding(length int) {
	b.offset += length
}

func (b *BlockReader) HasData() bool {
	return b.offset < len(b.data)
}

func (b *BlockReader) ReadLog() (sig uint32, subblock *BlockReader) {
	sig = b.Read32()
	size := int(b.Read32())
	hash := b.Read64()
	data := b.data[b.offset-16 : b.offset+size]
	b.offset += size
	if gothash := crc64.Checksum(data[16:], table); hash != gothash {
		log.Printf("bad checksum: expected 0x%x, got 0x%x\n", hash, gothash)
		return sig, nil
	}
	return sig, &BlockReader{data, 16}
}

func (br *BlockRef) Serialize(b Writer) {
	b.Write64(br.blockno)
	b.Write32(br.birth)
	b.Write32(br.children)
	b.Write32(br.size)
	b.WritePadding(4)
}

func DeserializeBlockRef(b Reader) BlockRef {
	blockno := b.Read64()
	birth := b.Read32()
	children := b.Read32()
	size := b.Read32()
	b.ReadPadding(4)
	return BlockRef{blockno, birth, children, size}
}
