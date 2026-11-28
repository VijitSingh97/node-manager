package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	utxoActiveName    = "ACTIVE"
	utxoRebuildPrefix = "rebuild-"
)

func (m *utxoManager) seedingInPlaceLocked() bool {
	return m.seeding && m.build == nil
}

func (m *utxoManager) seedStore() *utxoStore {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.build != nil {
		return m.build
	}
	return m.store
}

func (m *utxoManager) seedFollower() *utxoFollower {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.buildFol != nil {
		return m.buildFol
	}
	return m.follower
}

func (m *utxoManager) readActive() string {
	f, err := m.fs.Open(m.fs.PathJoin(m.dir, utxoActiveName))
	if err != nil {
		return ""
	}
	defer f.Close()
	raw, _ := io.ReadAll(io.LimitReader(f, 128))
	name := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(name, utxoRebuildPrefix) || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return ""
	}
	return name
}

func (m *utxoManager) writeActive(name string) error {
	tmp := m.fs.PathJoin(m.dir, utxoActiveName+".tmp")
	f, err := m.fs.Create(tmp)
	if err != nil {
		return err
	}
	if _, err = f.Write([]byte(name + "\n")); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = m.fs.Rename(tmp, m.fs.PathJoin(m.dir, utxoActiveName))
	}
	if err != nil {
		_ = m.fs.Remove(tmp)
		return err
	}
	if d, derr := m.fs.OpenDir(m.dir); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// settleLayoutLocked finds the active subdirectory, or "" for the volume root, and the leftovers of a finished switch or an unfinished rebuild to delete once the store opens.
func (m *utxoManager) settleLayoutLocked() (string, []string) {
	names, err := m.fs.List(m.dir)
	if err != nil {
		return "", nil
	}
	var drops []string
	active := m.readActive()
	found := false
	for _, n := range names {
		found = found || n == active
	}
	if active != "" && !found {
		m.log.Printf("utxo index: the active index directory %s is missing; using the volume root", active)
		active = ""
	}
	for _, n := range names {
		var drop bool
		if active != "" {
			drop = n != active && n != utxoActiveName
		} else {
			drop = strings.HasPrefix(n, utxoRebuildPrefix) || strings.HasPrefix(n, utxoActiveName)
		}
		if drop {
			drops = append(drops, n)
		}
	}
	return active, drops
}

func (m *utxoManager) removeLeftovers(names []string) {
	for _, n := range names {
		if err := m.fs.RemoveAll(m.fs.PathJoin(m.dir, n)); err != nil {
			m.log.Printf("utxo index: cannot remove %s: %v", n, err)
		}
	}
}

func (m *utxoManager) beginBuildLocked() error {
	name := fmt.Sprintf("%s%d", utxoRebuildPrefix, m.now().UnixNano())
	dir := m.fs.PathJoin(m.dir, name)
	_ = m.fs.RemoveAll(dir)
	if err := m.fs.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	s, err := openUtxoStoreFS(dir, m.fs, m.chain.ReverseIndex)
	if err != nil {
		_ = m.fs.RemoveAll(dir)
		return err
	}
	if !s.Valid() {
		_ = s.Close()
		_ = m.fs.RemoveAll(dir)
		return errUtxoBroken
	}
	f := newUtxoFollower(s, m.rpc)
	f.chain = m.chain
	m.build, m.buildDir, m.buildFol = s, name, f
	return nil
}

// settleBuild switches a finished rebuild in, or discards it; the serving index is untouched unless the switch commits.
func (m *utxoManager) settleBuild(err error) (error, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.build == nil {
		return err, false
	}
	build, name := m.build, m.buildDir
	m.build, m.buildFol, m.buildDir = nil, nil, ""
	if err == nil {
		return m.switchLocked(build, name), true
	}
	_ = build.Close()
	_ = m.fs.RemoveAll(m.fs.PathJoin(m.dir, name))
	return err, true
}

func (m *utxoManager) switchLocked(build *utxoStore, name string) error {
	err := build.Close()
	if err == nil {
		err = m.writeActive(name)
	}
	if err != nil {
		_ = m.fs.RemoveAll(m.fs.PathJoin(m.dir, name))
		return err
	}
	_ = m.store.Close()
	m.openLocked()
	return nil
}

// followWhileBuilding keeps the serving index following the chain while its replacement builds.
func (m *utxoManager) followWhileBuilding(parent context.Context) func() {
	m.mu.Lock()
	beside := m.build != nil
	m.mu.Unlock()
	if !beside || m.follower == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(utxoFollowInterval)
		defer t.Stop()
		for ctx.Err() == nil {
			m.tick(ctx)
			select {
			case <-ctx.Done():
			case <-t.C:
			case <-m.wake:
			}
		}
	}()
	return func() {
		cancel()
		<-stopped
	}
}
