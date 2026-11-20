package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Disk struct {
	Name       string   `json:"name"`
	Label      string   `json:"label,omitempty"`
	TotalBytes uint64   `json:"disk_total_bytes"`
	FreeBytes  uint64   `json:"disk_free_bytes"`
	Nodes      []string `json:"nodes"`
}

type mount struct {
	dev, point  string
	total, free uint64
}

type nodeDir struct{ service, dir string }

func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func readMounts(proc string, stat func(string) (uint64, uint64, bool)) []mount {
	text, _ := readText(filepath.Join(proc, "self/mounts"))
	var out []mount
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.HasPrefix(f[0], "/dev/") {
			continue
		}
		point := unescapeMount(f[1])
		used, avail, ok := stat(point)
		if !ok || used+avail == 0 {
			continue
		}
		out = append(out, mount{dev: f[0], point: point, total: used + avail, free: avail})
	}
	return out
}

func parentDisk(sys, name string, depth int) string {
	if depth > 4 || strings.HasPrefix(name, "loop") {
		return ""
	}
	if slaves, _ := os.ReadDir(filepath.Join(sys, "block", name, "slaves")); len(slaves) > 0 {
		return parentDisk(sys, slaves[0].Name(), depth+1)
	}
	if info, err := os.Stat(filepath.Join(sys, "block", name)); err == nil && info.IsDir() {
		return name
	}
	if m, _ := filepath.Glob(filepath.Join(sys, "block", "*", name)); len(m) > 0 {
		return filepath.Base(filepath.Dir(m[0]))
	}
	return ""
}

func diskLabel(sys, dev string) (phys, label string, ok bool) {
	name := filepath.Base(dev)
	if link, err := os.Readlink(dev); err == nil {
		name = filepath.Base(link)
	}
	phys = parentDisk(sys, name, 0)
	if phys == "" {
		return "", "", false
	}
	rota, _ := readText(filepath.Join(sys, "block", phys, "queue/rotational"))
	model, _ := readText(filepath.Join(sys, "block", phys, "device/model"))
	model = strings.Join(strings.Fields(model), " ")
	kind := "SSD"
	switch {
	case strings.HasPrefix(phys, "nvme"):
		kind = "NVMe"
	case strings.TrimSpace(rota) == "1":
		kind = "HDD"
	}
	switch {
	case model == "":
		label = kind + " " + phys
	case strings.Contains(strings.ToLower(model), strings.ToLower(kind)):
		label = model
	default:
		label = model + " " + kind
	}
	return phys, label, true
}

func buildDisks(sys string, mounts []mount, dirs []nodeDir) []Disk {
	type owner struct{ service, dev string }
	var owners []owner
	for _, d := range dirs {
		best := -1
		for i, m := range mounts {
			if d.dir == m.point || strings.HasPrefix(d.dir, m.point+"/") || m.point == "/" {
				if best < 0 || len(m.point) > len(mounts[best].point) {
					best = i
				}
			}
		}
		if best >= 0 {
			owners = append(owners, owner{d.service, mounts[best].dev})
		}
	}
	type named struct {
		mount
		phys, label string
	}
	seen := map[string]bool{}
	groups := map[string][]named{}
	for _, m := range mounts {
		if seen[m.dev] {
			continue
		}
		seen[m.dev] = true
		n := named{mount: m, phys: m.dev}
		if p, l, ok := diskLabel(sys, m.dev); ok {
			n.phys, n.label = p, l
		}
		groups[n.phys] = append(groups[n.phys], n)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	disks := []Disk{}
	for _, k := range keys {
		disk := Disk{Name: k, Nodes: []string{}}
		if !strings.HasPrefix(k, "/") {
			disk.Name = "/dev/" + k
		}
		member := map[string]bool{}
		for _, n := range groups[k] {
			disk.TotalBytes += n.total
			disk.FreeBytes += n.free
			member[n.dev] = true
			if disk.Label == "" {
				disk.Label = n.label
			}
		}
		have := map[string]bool{}
		for _, o := range owners {
			if member[o.dev] && !have[o.service] {
				have[o.service] = true
				disk.Nodes = append(disk.Nodes, o.service)
			}
		}
		sort.Strings(disk.Nodes)
		disks = append(disks, disk)
	}
	return disks
}
