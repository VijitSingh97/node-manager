package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func put(t *testing.T, root, rel, text string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyTree(t *testing.T, src, dst, root string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), []byte(strings.ReplaceAll(string(b), "@ROOT@", root)), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func dockerServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return socket
}

func staticDocker(t *testing.T, info DockerInfo, containers []Container) string {
	t.Helper()
	return dockerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/info":
			json.NewEncoder(w).Encode(info)
		case "/containers/json":
			json.NewEncoder(w).Encode(containers)
		default:
			if strings.HasPrefix(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json") {
				w.Write([]byte(`{"State":{"StartedAt":"2026-01-01T00:00:00.5Z"}}`))
				return
			}
			http.NotFound(w, r)
		}
	}))
}

func ctr(id, service, state string) Container {
	return Container{ID: id, State: state, Labels: map[string]string{
		"com.docker.compose.project": "p", "com.docker.compose.service": service,
	}}
}

type env struct {
	t    *testing.T
	root string
	cfg  Config
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{t: t, root: root}
	e.cfg = Config{
		ProcRoot: filepath.Join(root, "proc"), SysRoot: filepath.Join(root, "sys"), CgroupRoot: filepath.Join(root, "cg"),
		ThermalGlob: filepath.Join(root, "zones/*/temp"), StackRoot: filepath.Join(root, "stack"), StateRoot: filepath.Join(root, "state"),
		EnvFile: filepath.Join(root, "state/.env"), Project: "p", ClockTicks: 100,
		Statfs: func(string) (uint64, uint64, bool) { return 0, 0, false },
	}
	return e
}

func (e *env) put(rel, text string) { put(e.t, e.root, rel, text) }

func (e *env) cgroup(layout, id string, usage, anon uint64) {
	dir := "cg/system.slice/docker-" + id + ".scope"
	if layout == "cgroupfs" {
		dir = "cg/docker/" + id
	}
	put(e.t, e.root, dir+"/cpu.stat", "usage_usec "+strconv.FormatUint(usage, 10)+"\n")
	put(e.t, e.root, dir+"/memory.stat", "anon "+strconv.FormatUint(anon, 10)+"\nsock 10\n")
}

func (e *env) sampler(t *testing.T, containers []Container) *Sampler {
	t.Helper()
	socket := staticDocker(t, DockerInfo{NCPU: 4, MemTotal: 8 << 30, DockerRootDir: "/var/lib/docker"}, containers)
	return &Sampler{Cfg: e.cfg, Docker: NewDocker(socket), Now: func() time.Time { return time.Unix(1000, 0) },
		Sizes: &SizeCache{MaxAge: time.Hour, Run: func(string) (uint64, bool) { return 0, false }}}
}
