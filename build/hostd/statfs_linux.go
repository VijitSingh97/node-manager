package main

import "syscall"

func statfs(path string) (used, avail uint64, ok bool) {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return 0, 0, false
	}
	size := uint64(st.Frsize)
	if size == 0 {
		size = uint64(st.Bsize)
	}
	return (uint64(st.Blocks) - uint64(st.Bfree)) * size, uint64(st.Bavail) * size, true
}
