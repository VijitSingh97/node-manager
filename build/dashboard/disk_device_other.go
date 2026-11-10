//go:build !linux

package main

import "syscall"

func diskDevice(path string) string {
	var stat syscall.Statfs_t
	if syscall.Statfs(path, &stat) != nil {
		return ""
	}
	name := make([]byte, 0, len(stat.Mntfromname))
	for _, c := range stat.Mntfromname {
		if c == 0 {
			break
		}
		name = append(name, byte(c))
	}
	return string(name)
}
