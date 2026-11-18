package main

import (
	"os"
	"path/filepath"
	"strings"
)

func cgroupFile(root, id, name string) string {
	for _, dir := range []string{
		filepath.Join(root, "system.slice", "docker-"+id+".scope"),
		filepath.Join(root, "docker", id),
	} {
		path := filepath.Join(dir, name)
		if f, err := os.Open(path); err == nil {
			f.Close()
			return path
		}
	}
	return ""
}

func readUsageUsec(root, id string) (uint64, bool) {
	path := cgroupFile(root, id, "cpu.stat")
	if path == "" {
		return 0, false
	}
	text, _ := readText(path)
	for _, line := range strings.Split(text, "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "usage_usec" {
			return numPrefix(f[1]), true
		}
	}
	return 0, false
}

func readCgroupMem(root, id string) (uint64, bool) {
	path := cgroupFile(root, id, "memory.stat")
	if path == "" {
		return 0, false
	}
	text, _ := readText(path)
	var sum uint64
	seen := false
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "anon":
			seen = true
			sum += numPrefix(f[1])
		case "kernel_stack", "pagetables", "sec_pagetables", "percpu", "sock", "slab_unreclaimable":
			sum += numPrefix(f[1])
		}
	}
	return sum, seen
}
