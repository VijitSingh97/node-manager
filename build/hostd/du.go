package main

import (
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type sizeEntry struct {
	kb uint64
	at time.Time
}

type SizeCache struct {
	MaxAge time.Duration
	Now    func() time.Time
	Run    func(dir string) (uint64, bool)
	m      map[string]sizeEntry
}

func (c *SizeCache) Get(dir string) (uint64, bool) {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	if e, ok := c.m[dir]; ok && now().Sub(e.at) < c.MaxAge {
		return e.kb, true
	}
	kb, ok := c.Run(dir)
	if !ok {
		return 0, false
	}
	if c.m == nil {
		c.m = map[string]sizeEntry{}
	}
	c.m[dir] = sizeEntry{kb, now()}
	return kb, true
}

func duCommand(dir string) *exec.Cmd {
	args := []string{"du", "-sk", "--", dir}
	if _, err := exec.LookPath("nice"); err == nil {
		args = append([]string{"nice", "-n", "19"}, args...)
	}
	if _, err := exec.LookPath("ionice"); err == nil {
		args = append([]string{"ionice", "-c3"}, args...)
	}
	return exec.Command(args[0], args[1:]...)
}

func runDU(dir string) (uint64, bool) {
	out, _ := duCommand(dir).Output()
	f := strings.Fields(string(out))
	if len(f) == 0 || !digits(f[0]) {
		return 0, false
	}
	kb, err := strconv.ParseUint(f[0], 10, 64)
	return kb, err == nil
}
