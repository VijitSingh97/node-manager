package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func dropCache(dir string) {
	filepath.Walk(dir, func(p string, i os.FileInfo, err error) error {
		if err != nil || i.IsDir() {
			return nil
		}
		if f, err := os.Open(p); err == nil {
			unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
			f.Close()
		}
		return nil
	})
}
