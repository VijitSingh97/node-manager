package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	stateVersion = 1
	stateMaxSize = 1 << 20
	stateMaxAge  = 60 * time.Second
)

type State struct {
	Version int                 `json:"version"`
	SavedAt int64               `json:"saved_at_ns"`
	Prev    Snapshot            `json:"prev"`
	DU      map[string]duRecord `json:"du"`
}

func loadState(path string) *State {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, stateMaxSize+1))
	if err != nil || len(b) > stateMaxSize {
		return nil
	}
	var st State
	if json.Unmarshal(b, &st) != nil || st.Version != stateVersion {
		return nil
	}
	return &st
}

func saveState(path string, st *State) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func (st *State) usable(now time.Time, uptime float64, haveUptime bool) bool {
	age := now.Sub(time.Unix(0, st.SavedAt))
	return age > 0 && age < stateMaxAge && st.Prev.HaveUptime && haveUptime && uptime >= st.Prev.Uptime
}
