package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"testing"
	"time"
)

func TestUtxoManagerAppliesNewBlocksAtOnceRegtestLive(t *testing.T) {
	n := startLiveNode(t)
	n.do("createwallet", "w")
	mine := n.str("getnewaddress", "", "bech32")
	n.do("generatetoaddress", 101, mine)

	dir := t.TempDir()
	store, err := openUtxoStoreFS(dir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	genesis, _ := utxoRevHex(n.str("getblockhash", 0))
	if err := store.ApplyBlock(utxoBlock{Hash: genesis}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	m := newUtxoManager(dir, nil, n.rpc, log.New(io.Discard, "", 0))
	m.freeBytes = func() uint64 { return 1 << 40 }
	waitRPC := *n.rpc
	waitRPC.client = &http.Client{Timeout: 2 * time.Minute}
	m.blockWait, m.blockWaitRetry = &waitRPC, 100*time.Millisecond
	applied := make(chan struct{}, 8)
	m.onApplied = func() { applied <- struct{}{} }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	deadline := time.Now().Add(time.Minute)
	for st := m.Status(); st.State != utxoStateReady || *st.IndexTip != uint32(n.height()); st = m.Status() {
		if time.Now().After(deadline) {
			t.Fatalf("index did not catch up: %+v", st)
		}
		time.Sleep(100 * time.Millisecond)
	}
	for len(applied) > 0 {
		<-applied
	}
	time.Sleep(time.Second)

	for i := 0; i < 3; i++ {
		start := time.Now()
		n.do("generatetoaddress", 1, mine)
		want := uint32(n.height())
		for {
			select {
			case <-applied:
			case <-time.After(3*time.Second - time.Since(start)):
				t.Fatalf("block %d: not applied within 3 s: %+v", want, m.Status())
			}
			if st := m.Status(); st.State == utxoStateReady && *st.IndexTip == want {
				t.Logf("block %d applied %.2fs after it was mined", want, time.Since(start).Seconds())
				break
			}
		}
	}
}
