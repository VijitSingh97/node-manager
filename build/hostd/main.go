package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

var fchown = (*os.File).Chown

func chownToDirOwner(f *os.File, dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return fchown(f, int(st.Uid), int(st.Gid))
	}
	return nil
}

func writeStats(path string, st *Stats) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err = f.Chmod(0o600); err == nil {
		err = chownToDirOwner(f, filepath.Dir(path))
	}
	if err == nil {
		_, err = f.Write(b)
	}
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

type job struct {
	Cfg       Config
	Socket    string
	StateFile string
	Write     func(*Stats) error
	Wait      func()
	Now       func() time.Time
}

func run(cfg Config, socket, out string, wait func()) error {
	return job{Cfg: cfg, Socket: socket, Write: func(st *Stats) error { return writeStats(out, st) }, Wait: wait, Now: time.Now}.execute()
}

func (j job) execute() error {
	s := &Sampler{
		Cfg: j.Cfg, Docker: NewDocker(j.Socket), Now: j.Now,
		Sizes: &SizeCache{MaxAge: 10 * time.Minute, Now: j.Now, Run: runDU, Lazy: j.StateFile != ""},
	}
	var prev *Snapshot
	if j.StateFile != "" {
		if saved := loadState(j.StateFile); saved != nil {
			s.Sizes.Import(saved.DU)
			up, have := readUptime(j.Cfg.ProcRoot)
			if saved.usable(j.Now(), up, have) {
				prev = &saved.Prev
			}
		}
	}
	if prev == nil {
		first, err := s.Prime()
		if err != nil {
			return err
		}
		j.Wait()
		prev = first
	}
	st, snap, err := s.Sample(prev)
	if err != nil {
		return err
	}
	if j.StateFile != "" {
		next := &State{Version: stateVersion, SavedAt: j.Now().UnixNano(), Prev: *snap, DU: s.Sizes.Export()}
		if err := saveState(j.StateFile, next); err != nil {
			fmt.Fprintln(os.Stderr, "hostd: state:", err)
		}
	}
	return j.Write(st)
}

func volumeWriter(cfg Config, socket string) func(*Stats) error {
	return func(st *Stats) error {
		mount, err := NewDocker(socket).VolumeMountpoint(cfg.Project + "_dashboard_state")
		if err != nil {
			return err
		}
		return writeStatsVolume(mount, st)
	}
}

func mainRun(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("hostd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	once := fs.Bool("once", false, "sample, write the file and exit")
	out := fs.String("out", "", "path of host-stats.json")
	volume := fs.Bool("volume", false, "write host-stats.json into the dashboard state volume")
	stateFile := fs.String("state-file", "", "path of the carry-over state between runs")
	socket := fs.String("socket", "", "Docker socket (default $HOSTD_DOCKER_SOCKET or /var/run/docker.sock)")
	if fs.Parse(args) != nil {
		return 2
	}
	if !*once || (*out == "") == !*volume {
		fmt.Fprintln(stderr, "usage: hostd -once (-out PATH | -volume) [-state-file PATH] [-socket PATH]")
		return 2
	}
	if *socket == "" {
		if *socket = os.Getenv("HOSTD_DOCKER_SOCKET"); *socket == "" {
			*socket = "/var/run/docker.sock"
		}
	}
	cfg := ConfigFromEnv()
	write := func(st *Stats) error { return writeStats(*out, st) }
	if *volume {
		write = volumeWriter(cfg, *socket)
	}
	j := job{Cfg: cfg, Socket: *socket, StateFile: *stateFile, Write: write, Now: time.Now, Wait: func() { time.Sleep(time.Second) }}
	if err := j.execute(); errors.Is(err, ErrNoDashboard) {
		return 0
	} else if err != nil {
		fmt.Fprintln(stderr, "hostd:", err)
		return 1
	}
	return 0
}

func main() { os.Exit(mainRun(os.Args[1:], os.Stderr)) }
