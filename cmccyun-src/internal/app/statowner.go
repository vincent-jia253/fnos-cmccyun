package app

import (
	"io/fs"
	"syscall"
)

// statOwner 从 FileInfo 里取出 uid/gid（linux）。
func statOwner(st fs.FileInfo) ([2]uint32, bool) {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return [2]uint32{sys.Uid, sys.Gid}, true
	}
	return [2]uint32{}, false
}
