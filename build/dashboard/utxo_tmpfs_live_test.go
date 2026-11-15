package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const tmpfsLiveDir = "/idx"

func TestUtxoStorageFullTmpfsLive(t *testing.T) {
	if os.Getenv("UTXO_REGTEST") != "1" {
		t.Skip("set UTXO_REGTEST=1 (needs Docker) to fill a real size-limited tmpfs: UTXO_REGTEST=1 go test -run UtxoStorageFullTmpfsLive -v . from build/dashboard")
	}
	if os.Getenv("UTXO_TMPFS_IN_CONTAINER") == "1" {
		t.Skip("running inside the tmpfs container")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not running")
	}
	compose, err := os.ReadFile("../../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	image := regexp.MustCompile(`image: (bitcoin/bitcoin:\S+)`).FindStringSubmatch(string(compose))[1]
	arch, err := exec.Command("docker", "version", "--format", "{{.Server.Arch}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "test", "-c", "-o", filepath.Join(bin, "tmpfs.test"), ".")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+strings.TrimSpace(string(arch)), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the linux test binary: %v\n%s", err, out)
	}
	out, err := exec.Command("docker", "run", "--rm", "--tmpfs", tmpfsLiveDir+":size=6m,mode=1777", "-v", bin+":/t:ro",
		"-e", "UTXO_TMPFS_IN_CONTAINER=1", "--entrypoint", "/t/tmpfs.test", image,
		"-test.run", "^TestUtxoStorageFullTmpfsInContainer$", "-test.v", "-test.timeout", "5m").CombinedOutput()
	t.Logf("container output:\n%s", out)
	if err != nil {
		t.Fatalf("tmpfs test failed: %v", err)
	}
}

func TestUtxoStorageFullTmpfsInContainer(t *testing.T) {
	if os.Getenv("UTXO_TMPFS_IN_CONTAINER") != "1" {
		t.Skip("runs inside the container of TestUtxoStorageFullTmpfsLive")
	}
	ctx := context.Background()
	dir := filepath.Join(tmpfsLiveDir, "index")
	c := utxoMgrChain(40000, "b")
	node := newFakeUtxoNode(c)
	utxoMgrSeed(t, dir, nil, c)
	m := utxoMgrNew(t, dir, nil, node, io.Discard)

	realFree := func() uint64 { return diskUsage(dir).Free }
	const floor = 1 << 20
	m.freeBytes = func() uint64 { return realFree() + utxoMinFreeBytes - floor }

	ballast := filepath.Join(tmpfsLiveDir, "ballast")
	f, err := os.Create(ballast)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(f, zeroReader{}, int64(realFree()-floor/2)); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if realFree() >= floor {
		t.Fatalf("ballast left %d bytes free", realFree())
	}
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateDegraded {
		t.Fatalf("below the floor: %+v", st)
	}
	if err := os.Remove(ballast); err != nil {
		t.Fatal(err)
	}
	m.tick(ctx)
	if h, _, ok, _ := m.currentStore().Tip(); m.Status().State == utxoStateInvalid || !ok || h == 0 {
		t.Fatalf("space returned but index did not resume: %+v free=%d", m.Status(), realFree())
	}

	m.freeBytes = func() uint64 { return 1 << 40 }
	for i := 0; i < 400 && m.Status().State != utxoStateInvalid; i++ {
		m.tick(ctx)
	}
	st := m.Status()
	if st.State != utxoStateInvalid || st.Reason != "storage error while following the chain" {
		t.Fatalf("filling the volume did not mark the index invalid: %+v", st)
	}
	t.Logf("invalid after the kernel refused writes: %d bytes free, reason %q", realFree(), st.Reason)
	if realFree() > 1<<20 {
		t.Fatalf("volume is not full: %d bytes free", realFree())
	}
	m.tick(ctx)
	if _, _, _, ok := m.Lookup([][]byte{{0x00, 0x14}}); ok {
		t.Fatal("an invalid index served a lookup")
	}
	if m.Status().State != utxoStateInvalid {
		t.Fatalf("state changed after a further tick: %+v", m.Status())
	}

	_ = m.store.Close()
	again := utxoMgrNew(t, dir, nil, node, io.Discard)
	again.freeBytes = func() uint64 { return 1 << 40 }
	st = again.Status()
	t.Logf("reopened on the full volume: state %s", st.State)
	if st.State != utxoStateInvalid {
		t.Fatalf("the invalid state did not survive a reopen on the full volume: %+v", st)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
