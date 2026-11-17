package main

import (
	"encoding/json"
	"flag"
	"fmt"
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

func run(cfg Config, socket, out string, wait func()) error {
	s := &Sampler{
		Cfg: cfg, Docker: NewDocker(socket), Now: time.Now,
		Sizes: &SizeCache{MaxAge: 10 * time.Minute, Run: runDU},
	}
	first, err := s.Prime()
	if err != nil {
		return err
	}
	wait()
	st, _, err := s.Sample(first)
	if err != nil {
		return err
	}
	return writeStats(out, st)
}

func main() {
	once := flag.Bool("once", false, "sample twice about one second apart, write the file and exit")
	out := flag.String("out", "", "path of host-stats.json")
	socket := flag.String("socket", "", "Docker socket (default $HOSTD_DOCKER_SOCKET or /var/run/docker.sock)")
	flag.Parse()
	if !*once || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: hostd -once -out PATH [-socket PATH]")
		os.Exit(2)
	}
	if *socket == "" {
		if *socket = os.Getenv("HOSTD_DOCKER_SOCKET"); *socket == "" {
			*socket = "/var/run/docker.sock"
		}
	}
	if err := run(ConfigFromEnv(), *socket, *out, func() { time.Sleep(time.Second) }); err != nil {
		fmt.Fprintln(os.Stderr, "hostd:", err)
		os.Exit(1)
	}
}
