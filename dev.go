package onetreefs

import (
	"log"
	"os"
	"syscall"
)

type BlockDevice interface {
	Read(offset uint64, length uint32) (*BlockReader, syscall.Errno)
	Write(offset uint64, data []byte) syscall.Errno
	Flush() syscall.Errno
}

type FileDevice struct {
	f *os.File
}

type RamDevice struct {
	// really just for testing
	data []byte
}

func (d *FileDevice) Read(offset uint64, length uint32) (*BlockReader, syscall.Errno) {
	data := make([]byte, length)
	n, err := d.f.ReadAt(data, int64(offset))
	if err != nil {
		log.Printf("IO error reading data: %s\n", err)
		return nil, syscall.EIO
	}
	if uint32(n) != length {
		log.Printf("Short read: expected %d bytes got %d\n", length, n)
		return nil, syscall.EIO
	}
	return &BlockReader{data, 0}, 0
}

func ReadLog(d BlockDevice, offset uint64, sig uint32) (*BlockReader, uint32, syscall.Errno) {
	b, err := d.Read(offset, 16)
	if err != 0 {
		return nil, 0, err
	}
	if gotsig := b.Read32(); gotsig == 0 {
		return nil, 0, 0
	} else if gotsig != sig {
		log.Printf("bad signature 0x%x (expected 0x%x) at %d\n", gotsig, sig, offset)
		return nil, 0, syscall.EIO // TODO this error?
	}
	size := b.Read32()
	hash := b.Read64()
	b, err = d.Read(offset+16, size)
	if err != 0 {
		return nil, 0, err
	}
	if gothash := b.Hash(); gothash != hash {
		log.Printf("bad hash 0x%x (expected 0x%x) at %d\n", gothash, hash, offset)
		return nil, 0, syscall.EIO // TODO this error?
	}
	return b, size + 16, 0
}

func (d *FileDevice) Write(offset uint64, data []byte) syscall.Errno {
	n, err := d.f.WriteAt(data, int64(offset))
	if err != nil {
		log.Printf("IO error writing data: %s\n", err)
		return syscall.EIO
	}
	if n != len(data) {
		log.Printf("Short write: expected %d bytes got %d\n", len(data), n)
		return syscall.EIO
	}
	return 0
}

func (d *FileDevice) Flush() syscall.Errno {
	err := d.f.Sync()
	if err != nil {
		log.Printf("IO error syncing data: %s\n", err)
		return syscall.EIO
	}
	return 0
}

func OpenFileDevice(filename string) (*FileDevice, error) {
	if f, e := os.OpenFile(filename, os.O_RDWR, 0644); e != nil {
		return nil, e
	} else {
		return &FileDevice{f}, nil
	}
}

func CreateFileDevice(filename string) (*FileDevice, error) {
	if f, e := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0644); e != nil {
		return nil, e
	} else {
		return &FileDevice{f}, nil
	}
}

func CreateRamDevice() *RamDevice {
	return &RamDevice{nil}
}

func (d *RamDevice) Read(offset uint64, length uint32) (*BlockReader, syscall.Errno) {
	return &BlockReader{d.data[offset : offset+uint64(length)], 0}, 0
}

func (d *RamDevice) Write(offset uint64, data []byte) syscall.Errno {
	if offset > uint64(len(d.data)) {
		d.data = append(d.data, make([]byte, offset+uint64(len(data)))...)
	}
	copied := copy(d.data[offset:], data)
	if copied < len(data) {
		d.data = append(d.data, data[copied:]...)
	}
	return 0
}

func (d *RamDevice) Flush() syscall.Errno {
	return 0
}
