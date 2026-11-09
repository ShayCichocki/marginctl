//go:build unix

package fsutil

import (
	"os"
	"syscall"
)

// FileIdentity is the stable filesystem object identity available on Unix.
type FileIdentity struct {
	Device uint64
	Inode  uint64
	Valid  bool
}

func fileIdentity(info os.FileInfo) FileIdentity {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return FileIdentity{}
	}
	return FileIdentity{Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Valid: true}
}
