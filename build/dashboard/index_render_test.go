package main

import (
	"bytes"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const renderedIndexFixture = "testdata/index.rendered.html"

func TestRenderedIndexFixtureIsCurrent(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{dashboardPassword: "dashboard-secret", dataDir: t.TempDir(), stateDir: t.TempDir(), version: "1.31.1"}
	a, err := newApp(cfg, &rpcClient{}, filepath.Join(root, "index.html"), filepath.Join(root, "static"), log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.render(w, pageData{BitcoinEnabled: true, Version: "v1.2.3"})
	if os.Getenv("UPDATE_INDEX_FIXTURE") != "" {
		if err := os.WriteFile(renderedIndexFixture, w.Body.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(renderedIndexFixture)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, w.Body.Bytes()) {
		t.Fatalf("%s is stale; regenerate with UPDATE_INDEX_FIXTURE=1 go test -run TestRenderedIndexFixtureIsCurrent", renderedIndexFixture)
	}
}
