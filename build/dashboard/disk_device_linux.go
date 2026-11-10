//go:build linux

package main

import "os"

func diskDevice(path string) string {
	b, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return ""
	}
	return mountDevice(string(b), path)
}
