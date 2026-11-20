package main

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var duTimeout = 45 * time.Second

type sizeEntry struct {
	kb    uint64
	at    time.Time
	tried time.Time
}

func (e sizeEntry) measured() bool { return e.at.Unix() > 0 }

func (e sizeEntry) lastAttempt() time.Time {
	if e.tried.After(e.at) {
		return e.tried
	}
	return e.at
}

type SizeCache struct {
	MaxAge time.Duration
	Now    func() time.Time
	Run    func(dir string) (uint64, bool)
	Lazy   bool
	m      map[string]sizeEntry
}

type duRecord struct {
	KB         uint64 `json:"kb"`
	MeasuredAt int64  `json:"measured_at"`
	TriedAt    int64  `json:"tried_at,omitempty"`
}

func (c *SizeCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *SizeCache) Export() map[string]duRecord {
	out := make(map[string]duRecord, len(c.m))
	for dir, e := range c.m {
		out[dir] = duRecord{e.kb, e.at.Unix(), e.tried.Unix()}
	}
	return out
}

func (c *SizeCache) Import(in map[string]duRecord) {
	c.m = make(map[string]sizeEntry, len(in))
	for dir, r := range in {
		c.m[dir] = sizeEntry{r.KB, time.Unix(r.MeasuredAt, 0), time.Unix(r.TriedAt, 0)}
	}
}

func (c *SizeCache) RefreshOne(dirs []string) {
	now := c.now()
	pick, oldest := "", time.Time{}
	for _, dir := range dirs {
		e, ok := c.m[dir]
		last := e.lastAttempt()
		if ok && now.Sub(last) <= c.MaxAge {
			continue
		}
		if pick == "" || last.Before(oldest) {
			pick, oldest = dir, last
		}
	}
	if pick == "" {
		return
	}
	if c.m == nil {
		c.m = map[string]sizeEntry{}
	}
	e := c.m[pick]
	if kb, ok := c.Run(pick); ok {
		e = sizeEntry{kb: kb, at: now}
	} else {
		e.tried = now
	}
	c.m[pick] = e
}

func (c *SizeCache) Get(dir string) (uint64, bool) {
	now := c.now
	if e, ok := c.m[dir]; ok && e.measured() && (c.Lazy || now().Sub(e.at) < c.MaxAge) {
		return e.kb, true
	}
	if c.Lazy {
		return 0, false
	}
	kb, ok := c.Run(dir)
	if !ok {
		return 0, false
	}
	if c.m == nil {
		c.m = map[string]sizeEntry{}
	}
	c.m[dir] = sizeEntry{kb: kb, at: now()}
	return kb, true
}

func duCommand(ctx context.Context, dir string) *exec.Cmd {
	args := []string{"du", "-sk", "--", dir}
	if _, err := exec.LookPath("nice"); err == nil {
		args = append([]string{"nice", "-n", "19"}, args...)
	}
	if _, err := exec.LookPath("ionice"); err == nil {
		args = append([]string{"ionice", "-c3"}, args...)
	}
	return exec.CommandContext(ctx, args[0], args[1:]...)
}

func runDU(dir string) (uint64, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), duTimeout)
	defer cancel()
	out, _ := duCommand(ctx, dir).Output()
	f := strings.Fields(string(out))
	if len(f) == 0 || !digits(f[0]) {
		return 0, false
	}
	kb, err := strconv.ParseUint(f[0], 10, 64)
	return kb, err == nil
}
