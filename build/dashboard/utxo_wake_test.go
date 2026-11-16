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

func waitForCalls(t *testing.T, w *waitBlockRPC, n int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(w.calls()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("waitfornewblock calls: %v, want %d", w.calls(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return w.calls()
}

func TestUtxoWaitBlocksWakesTheFollowerOnANewTip(t *testing.T) {
	w := &waitBlockRPC{results: make(chan string)}
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(3, "w")), io.Discard)
	m.blockWait = w
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.waitBlocks(ctx)

	w.results <- "aa"
	select {
	case <-m.wake:
	case <-time.After(2 * time.Second):
		t.Fatal("a new tip did not wake the follower")
	}
	w.results <- "aa"
	got := waitForCalls(t, w, 3)
	select {
	case <-m.wake:
		t.Fatal("a timeout on the same tip woke the follower")
	default:
	}
	if want := []string{"[60000]", "[60000 aa]", "[60000 aa]"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("waitfornewblock params: %v, want %v", got, want)
	}
	w.results <- "bb"
	select {
	case <-m.wake:
	case <-time.After(2 * time.Second):
		t.Fatal("the next block did not wake the follower")
	}

	waitForCalls(t, w, 4)
	w.results <- ""
	time.Sleep(300 * time.Millisecond)
	if n := len(w.calls()); n != 4 {
		t.Fatalf("an RPC error was retried at once: %d calls", n)
	}
}

func TestUtxoManagerSignalsWalletsWhenTheIndexReachesANewBlock(t *testing.T) {
	ctx := context.Background()
	c := utxoMgrChain(10, "n")
	dir := t.TempDir()
	utxoMgrSeed(t, dir, nil, c)
	m := utxoMgrNew(t, dir, nil, newFakeUtxoNode(c), io.Discard)
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
	utxoMgrAddBlock(c, "n", 11)
	m.freeBytes = func() uint64 { return utxoMinFreeBytes - 1 }
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateDegraded || applied != 2 {
		t.Fatalf("degraded index: state %s, signals %d", st.State, applied)
	}
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
