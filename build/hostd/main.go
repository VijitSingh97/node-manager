package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

var fchown = (*os.File).Chown

type job struct {
	Cfg       Config
	Socket    string
	StateFile string
	Write     func(*Stats) error
	Wait      func()
	Now       func() time.Time
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
	once := fs.Bool("once", false, "sample, write the stats and exit")
	volume := fs.Bool("volume", false, "write host-stats.json into the dashboard state volume")
	stateFile := fs.String("state-file", "", "path of the carry-over state between runs")
	if fs.Parse(args) != nil {
		return 2
	}
	if !*once || !*volume {
		fmt.Fprintln(stderr, "usage: hostd -once -volume [-state-file PATH]")
		return 2
	}
	socket := os.Getenv("HOSTD_DOCKER_SOCKET")
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	cfg := ConfigFromEnv()
	j := job{Cfg: cfg, Socket: socket, StateFile: *stateFile, Write: volumeWriter(cfg, socket), Now: time.Now, Wait: func() { time.Sleep(time.Second) }}
	if err := j.execute(); errors.Is(err, ErrNoDashboard) {
		return 0
	} else if err != nil {
		fmt.Fprintln(stderr, "hostd:", err)
		return 1
	}
	return 0
}

func main() { os.Exit(mainRun(os.Args[1:], os.Stderr)) }
