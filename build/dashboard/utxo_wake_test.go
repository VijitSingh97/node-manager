package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

type waitBlockRPC struct {
	mu      sync.Mutex
	params  []string
	results chan string
}

func (w *waitBlockRPC) call(ctx context.Context, method string, params any, out any) error {
	if method != "waitfornewblock" {
		return errors.New("unexpected " + method)
	}
	w.mu.Lock()
	w.params = append(w.params, fmt.Sprint(params))
	w.mu.Unlock()
	select {
	case h := <-w.results:
		if h == "" {
			return errors.New("rpc error")
		}
		return json.Unmarshal([]byte(`{"hash":"`+h+`","height":1}`), out)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *waitBlockRPC) calls() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.params...)
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestUtxoWaitBlocksWakesTheFollowerOnANewTip(t *testing.T) {
	w := &waitBlockRPC{results: make(chan string)}
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(3, "w")), io.Discard)
	m.blockWait, m.blockWaitRetry = w, 50*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.waitBlocks(ctx)

	time.Sleep(200 * time.Millisecond)
	if n := len(w.calls()); n != 0 {
		t.Fatalf("waited on bitcoind while the index was not ready: %d calls", n)
	}
	m.set(utxoStateReady, "")
	w.results <- "aa"
	select {
	case <-m.wake:
	case <-time.After(2 * time.Second):
		t.Fatal("a new tip did not wake the follower")
	}
	w.results <- "aa"
	waitUntil(t, "no third waitfornewblock call", func() bool { return len(w.calls()) >= 3 })
	select {
	case <-m.wake:
		t.Fatal("a timeout on the same tip woke the follower")
	default:
	}
	w.results <- "bb"
	select {
	case <-m.wake:
	case <-time.After(2 * time.Second):
		t.Fatal("the next block did not wake the follower")
	}
	waitUntil(t, "no fourth waitfornewblock call", func() bool { return len(w.calls()) >= 4 })
	w.results <- ""
	waitUntil(t, "no retry after the RPC error", func() bool { return len(w.calls()) >= 5 })
	if want := "[[60000] [60000 aa] [60000 aa] [60000 bb] [60000 bb]]"; fmt.Sprint(w.calls()) != want {
		t.Fatalf("waitfornewblock params: %v, want %v", w.calls(), want)
	}
}

func TestUtxoManagerSignalsWalletsWhenTheIndexReachesANewBlock(t *testing.T) {
	ctx := context.Background()
	c := utxoMgrChain(10, "n")
	node := newFakeUtxoNode(c)
	dir := t.TempDir()
	utxoMgrSeed(t, dir, nil, c)
	m := utxoMgrNew(t, dir, nil, node, io.Discard)
	applied := 0
	m.onApplied = func() { applied++ }

	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateReady || applied != 1 {
		t.Fatalf("catch-up to ready: state %s, signals %d", st.State, applied)
	}
	m.tick(ctx)
	if applied != 1 {
		t.Fatalf("a tick with no new block signalled wallets: %d", applied)
	}
	utxoMgrAddBlock(c, "n", 10)
	m.tick(ctx)
	if st := m.Status(); *st.IndexTip != 10 || applied != 2 {
		t.Fatalf("new block: tip %d, signals %d", *st.IndexTip, applied)
	}
	fork := utxoMgrChain(11, "y")
	node.fakeUtxoChain = fork
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateReady || *st.IndexTip != 10 || applied != 3 {
		t.Fatalf("same-height reorg: state %s, tip %d, signals %d", st.State, *st.IndexTip, applied)
	}
	utxoMgrAddBlock(fork, "y", 11)
	m.freeBytes = func() uint64 { return utxoMinFreeBytes - 1 }
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateDegraded || applied != 3 {
		t.Fatalf("degraded index: state %s, signals %d", st.State, applied)
	}
}

func TestUtxoManagerRunAppliesABlockWhenBitcoindReportsIt(t *testing.T) {
	c := utxoMgrChain(10, "r")
	dir := t.TempDir()
	utxoMgrSeed(t, dir, nil, c)
	m := utxoMgrNew(t, dir, nil, newFakeUtxoNode(c), io.Discard)
	w := &waitBlockRPC{results: make(chan string)}
	m.blockWait, m.blockWaitRetry = w, 50*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	waitUntil(t, "index never became ready", func() bool {
		st := m.Status()
		return st.State == utxoStateReady && *st.IndexTip == 9
	})
	waitUntil(t, "no waitfornewblock call", func() bool { return len(w.calls()) >= 1 })
	utxoMgrAddBlock(c, "r", 10)
	w.results <- "new-tip"
	waitUntil(t, "the reported block was not applied before the 10 s ticker", func() bool {
		return *m.Status().IndexTip == 10
	})
}

func TestWatchRefreshRunsWhenTheIndexAppliesABlock(t *testing.T) {
	fixture := &rpcFixture{}
	a, _ := testApp(t, fixture, "")
	a.watchWake = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.refreshWatchLoop(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	waitUntil(t, "the first refresh made no RPC", func() bool { return fixture.count() > 0 })
	last := -1
	waitUntil(t, "the first refresh did not settle", func() bool {
		n := fixture.count()
		settled := n == last
		last = n
		time.Sleep(100 * time.Millisecond)
		return settled
	})
	a.watchWake <- struct{}{}
	waitUntil(t, "a wake did not refresh the wallets", func() bool { return fixture.count() > last })
}

func TestEveryOrWakeRunsAgainOnWake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := make(chan struct{}, 1)
	runs := make(chan struct{}, 4)
	go (&app{}).everyOrWake(ctx, time.Hour, wake, func(context.Context) { runs <- struct{}{} })
	for i := 0; i < 2; i++ {
		select {
		case <-runs:
		case <-time.After(2 * time.Second):
			t.Fatalf("run %d did not happen", i+1)
		}
		wake <- struct{}{}
	}
}
