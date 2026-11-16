package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	seedLiveDatadir = "/home/bitcoin/.bitcoin"
	seedLiveMount   = "/data"
	seedLiveNetDir  = "/regtest"
)

func seedLiveDocker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestUtxoSeedRegtestLive(t *testing.T) {
	if os.Getenv("UTXO_REGTEST") != "1" {
		t.Skip("set UTXO_REGTEST=1 to run against the pinned regtest bitcoind")
	}
	if os.Getenv("UTXO_SEED_IN_CONTAINER") == "1" {
		t.Skip("running inside the reader container")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not running")
	}
	vol := fmt.Sprintf("utxo-seed-live-%d", time.Now().UnixNano())
	seedLiveDocker(t, "volume", "create", vol)
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", vol).Run() })
	n := startLiveNodeWith(t, "-v", vol+":"+seedLiveDatadir)
	image := seedLiveDocker(t, "inspect", n.name, "--format", "{{.Config.Image}}")
	uid := seedLiveDocker(t, "exec", n.name, "id", "-u", "bitcoin")
	arch := seedLiveDocker(t, "version", "--format", "{{.Server.Arch}}")

	n.do("createwallet", "w")
	types := []string{"legacy", "p2sh-segwit", "bech32", "bech32m"}
	var addrs, scripts []string
	for i := 0; i < 8; i++ {
		a, s := n.newScript(types[i%len(types)])
		addrs, scripts = append(addrs, a), append(scripts, s)
	}
	mine, mineScript := n.newScript("bech32")
	scripts = append(scripts, mineScript)
	n.do("generatetoaddress", 101, mine)
	many := map[string]any{}
	for _, a := range addrs {
		many[a] = "0.01"
	}
	for i := 0; i < 4; i++ {
		n.do("sendmany", "", many)
		n.do("generatetoaddress", 1, mine)
	}

	bin := t.TempDir()
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "test", "-c", "-o", filepath.Join(bin, "seed.test"), ".")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the linux test binary: %v\n%s", err, out)
	}

	seedLiveDocker(t, "exec", "-u", "bitcoin", n.name, "mkfifo", "-m", "600", seedLiveDatadir+seedLiveNetDir+"/"+seedIncompleteName)
	name := n.name + "-reader"
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", "-v", name).Run() })
	out, err := exec.Command("docker", "run", "--rm", "--name", name, "--user", uid, "--network", "container:"+n.name,
		"-v", vol+":"+seedLiveMount+":ro", "-v", bin+":/t:ro",
		"-e", "UTXO_SEED_IN_CONTAINER=1", "-e", "SEED_SCRIPTS="+strings.Join(scripts, ","), "-e", "SEED_MINE="+mine,
		"--entrypoint", "/t/seed.test", image,
		"-test.run", "^TestUtxoSeedInContainer$", "-test.v", "-test.timeout", "5m").CombinedOutput()
	t.Logf("reader container output:\n%s", out)
	if err != nil {
		t.Fatalf("seed inside the container failed: %v", err)
	}
	if n.height() < 105 {
		t.Fatalf("node height %d", n.height())
	}
}

func TestUtxoSeedInContainer(t *testing.T) {
	if os.Getenv("UTXO_SEED_IN_CONTAINER") != "1" {
		t.Skip("runs inside the reader container of TestUtxoSeedRegtestLive")
	}
	rpc := &rpcClient{url: "http://127.0.0.1:18443", user: "test", password: "test",
		client: &http.Client{Timeout: 2 * time.Minute}, maxBody: 64 << 20}
	dump := *rpc
	dump.client = &http.Client{}
	n := &liveNode{t: t, rpc: rpc}
	m := newUtxoManager(t.TempDir(), nil, rpc, log.New(io.Discard, "", 0), utxoBitcoin)
	state := t.TempDir()
	m.configureSeed(utxoSeedHost{stateDir: state, dataDir: seedLiveMount + seedLiveNetDir, dump: &dump,
		hold: func() bool { return true }, release: func() {}})
	m.setSeedStatePath(filepath.Join(state, "utxo-seed.json"))
	m.temp = func() (float64, error) { return 50, nil }
	m.guards = func(ctx context.Context) seedGuardInput {
		in := m.gatherSeedGuards(ctx)
		in.TempC, in.TempErr, in.CPUs, in.CPUErr, in.FreeBytes, in.FreeErr = 50, nil, 0.5, nil, 1<<40, nil
		return in
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	id := seedTestRequestID(1)
	raw := fmt.Sprintf(`{"id":%q,"requested_at":%d}`, id, time.Now().Unix())
	if err := os.WriteFile(filepath.Join(state, seedRequestName), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor := func(what string, limit time.Duration, ok func() bool) {
		t.Helper()
		for end := time.Now().Add(limit); !ok(); time.Sleep(200 * time.Millisecond) {
			if time.Now().After(end) {
				t.Fatalf("timed out waiting for %s: %+v", what, m.Status())
			}
		}
	}
	waitFor("the seed to finish", 3*time.Minute, func() bool { return m.Status().LastSeed != nil })
	if st := m.Status(); st.LastSeed.Outcome != "ok" {
		t.Fatalf("seed outcome %+v refusals %v", st.LastSeed, st.Refusals)
	}
	if fi, err := os.Lstat(filepath.Join(seedLiveMount+seedLiveNetDir, seedDumpName)); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("Core did not rename the pipe to the dump name: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(seedLiveMount+seedLiveNetDir, seedIncompleteName)); !os.IsNotExist(err) {
		t.Fatalf("incomplete pipe still present: %v", err)
	}

	n.do("generatetoaddress", 1, os.Getenv("SEED_MINE"))
	waitFor("the follower to reach the new tip", 90*time.Second, func() bool {
		st := m.Status()
		return st.State == utxoStateReady && st.Behind != nil && *st.Behind == 0 && st.IndexTip != nil && int(*st.IndexTip) == n.height()
	})

	checked := 0
	for _, script := range strings.Split(os.Getenv("SEED_SCRIPTS"), ",") {
		want := n.scan(script)
		got := lookupAmounts(t, m.currentStore(), script)
		if len(got) != len(want) {
			t.Fatalf("script %s: index has %d coins, scantxoutset %d", script, len(got), len(want))
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("script %s coin %s: index %d, scantxoutset %d", script, k, got[k], v)
			}
		}
		checked += len(want)
	}
	if checked < 20 {
		t.Fatalf("only %d coins compared", checked)
	}
	t.Logf("seeded through a real FIFO as uid %d and matched scantxoutset on %d coins", os.Getuid(), checked)
}
