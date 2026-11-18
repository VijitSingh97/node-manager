package main

import (
	"encoding/json"
	"errors"
	"os"
	"syscall"
)

const (
	statsName = "host-stats.json"
	statsTmp  = statsName + ".tmp"
)

func writeStatsVolume(mountpoint string, st *Stats) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	before, err := os.Lstat(mountpoint)
	if err != nil {
		return err
	}
	if !before.IsDir() {
		return errors.New("volume mountpoint is not a directory")
	}
	root, err := os.OpenRoot(mountpoint)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(before, dir) {
		return errors.New("volume mountpoint changed while opening")
	}
	owner, ok := dir.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("volume owner unavailable")
	}
	if err := root.Remove(statsTmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := root.OpenFile(statsTmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	err = fchown(f, int(owner.Uid), int(owner.Gid))
	if err == nil {
		_, err = f.Write(b)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Rename(statsTmp, statsName)
	}
	if err != nil {
		root.Remove(statsTmp)
	}
	return err
}
