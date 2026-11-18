package main

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func readText(path string) (string, bool) {
	b, err := os.ReadFile(path)
	return string(b), err == nil
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func roundTo(v float64, places int) float64 {
	r, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', places, 64), 64)
	return r
}

func numPrefix(s string) uint64 {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	v, _ := strconv.ParseUint(s[:n], 10, 64)
	return v
}

func readCPU(proc string) (total, idle uint64, ok bool) {
	text, ok := readText(filepath.Join(proc, "stat"))
	if !ok {
		return 0, 0, false
	}
	line, _, _ := strings.Cut(text, "\n")
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "cpu" {
		return 0, 0, false
	}
	for i, f := range fields[1:] {
		v := numPrefix(f)
		total += v
		if i == 3 || i == 4 {
			idle += v
		}
	}
	return total, idle, true
}

func readMemUsed(proc string) (uint64, bool) {
	text, ok := readText(filepath.Join(proc, "meminfo"))
	if !ok {
		return 0, false
	}
	var total, avail uint64
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total = numPrefix(f[1])
		case "MemAvailable:":
			avail = numPrefix(f[1])
		}
	}
	if avail > total {
		return 0, true
	}
	return (total - avail) * 1024, true
}

func readUptime(proc string) (float64, bool) {
	text, ok := readText(filepath.Join(proc, "uptime"))
	f := strings.Fields(text)
	if !ok || len(f) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil || v < 0 || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, false
	}
	return v, true
}

func physIfaces(sys string) map[string]bool {
	out := map[string]bool{}
	matches, _ := filepath.Glob(filepath.Join(sys, "class/net/*/device"))
	for _, m := range matches {
		if _, err := os.Stat(m); err == nil {
			out[filepath.Base(filepath.Dir(m))] = true
		}
	}
	return out
}

func readNet(proc string, phys map[string]bool) (rx, tx uint64, ok bool) {
	if len(phys) == 0 {
		return 0, 0, false
	}
	text, ok := readText(filepath.Join(proc, "net/dev"))
	if !ok {
		return 0, 0, false
	}
	lines := strings.Split(text, "\n")
	if len(lines) < 3 {
		return 0, 0, false
	}
	n := 0
	for _, line := range lines[2:] {
		name, rest, found := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !found || !phys[name] {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx += numPrefix(f[0])
		tx += numPrefix(f[8])
		n++
	}
	return rx, tx, n > 0
}

func readThermal(glob string) (cpu, board *float64) {
	var cpuMilli, boardMilli int64 = -1, -1
	for _, pattern := range strings.Fields(glob) {
		matches, _ := filepath.Glob(pattern)
		for _, temp := range matches {
			text, ok := readText(temp)
			text = strings.TrimSpace(text)
			if !ok || !digits(text) {
				continue
			}
			milli, err := strconv.ParseInt(text, 10, 64)
			if err != nil {
				continue
			}
			kind, _ := readText(filepath.Join(filepath.Dir(temp), "type"))
			var key string
			if f := strings.Fields(kind); len(f) > 0 {
				key = f[0]
			}
			switch {
			case key == "acpitz":
				boardMilli = max(boardMilli, milli)
			case key == "x86_pkg_temp" || key == "TCPU" || isThermalPrefix(key):
				cpuMilli = max(cpuMilli, milli)
			}
		}
	}
	celsius := func(milli int64) *float64 {
		if milli < 0 {
			return nil
		}
		v := roundTo(float64(milli)/1000, 1)
		return &v
	}
	return celsius(cpuMilli), celsius(boardMilli)
}

func isThermalPrefix(key string) bool {
	for _, p := range []string{"cpu", "soc"} {
		if key == p+"-thermal" || key == p+"_thermal" {
			return true
		}
	}
	return false
}

func readFans(sys string) (rpm, maxRPM []int) {
	var fans []string
	for _, pattern := range []string{"class/hwmon/hwmon*/fan*_input", "class/hwmon/hwmon*/device/fan*_input"} {
		matches, _ := filepath.Glob(filepath.Join(sys, pattern))
		fans = append(fans, matches...)
	}
	for _, fan := range fans {
		if info, err := os.Stat(fan); err != nil || !info.Mode().IsRegular() {
			continue
		}
		text, ok := readText(fan)
		text = strings.TrimSpace(text)
		if !ok || !digits(text) {
			continue
		}
		v, err := strconv.Atoi(text)
		if err != nil {
			continue
		}
		limit := 0
		if m, ok := readText(strings.TrimSuffix(fan, "_input") + "_max"); ok {
			if m = strings.TrimSpace(m); digits(m) {
				limit, _ = strconv.Atoi(m)
			}
		}
		rpm = append(rpm, v)
		maxRPM = append(maxRPM, limit)
	}
	return rpm, maxRPM
}
