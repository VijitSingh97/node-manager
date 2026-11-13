package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type tariFixture struct {
	UnimplementedBaseNodeServer
	timestamp uint64
}

type tariNoNetworkFixture struct{ tariFixture }

type tariSyncingFixture struct{ tariFixture }

type tariUnprunedFixture struct{ tariFixture }

type tariGenesisFixture struct{ tariFixture }

func (f tariGenesisFixture) GetTipInfo(context.Context, *emptypb.Empty) (*TipInfoResponse, error) {
	return &TipInfoResponse{Metadata: &MetaData{Timestamp: f.timestamp}, InitialSyncAchieved: true, BaseNodeState: BaseNodeState_LISTENING}, nil
}

func (tariGenesisFixture) GetSyncProgress(context.Context, *emptypb.Empty) (*SyncProgressResponse, error) {
	return &SyncProgressResponse{State: SyncState_DONE}, nil
}

func (tariSyncingFixture) GetSyncProgress(context.Context, *emptypb.Empty) (*SyncProgressResponse, error) {
	return &SyncProgressResponse{TipHeight: 200, LocalHeight: 123, State: SyncState_BLOCK}, nil
}

func (f tariUnprunedFixture) GetTipInfo(context.Context, *emptypb.Empty) (*TipInfoResponse, error) {
	return &TipInfoResponse{Metadata: &MetaData{BestBlockHeight: 123, Timestamp: f.timestamp}, InitialSyncAchieved: true, BaseNodeState: BaseNodeState_LISTENING}, nil
}

func (tariNoNetworkFixture) GetNetworkState(context.Context, *GetNetworkStateRequest) (*GetNetworkStateResponse, error) {
	return nil, errors.New("unavailable")
}

func (f tariFixture) GetTipInfo(context.Context, *emptypb.Empty) (*TipInfoResponse, error) {
	return &TipInfoResponse{Metadata: &MetaData{BestBlockHeight: 123, PrunedHeight: 10, Timestamp: f.timestamp}, InitialSyncAchieved: true, BaseNodeState: BaseNodeState_LISTENING}, nil
}
func (tariFixture) GetSyncProgress(context.Context, *emptypb.Empty) (*SyncProgressResponse, error) {
	return &SyncProgressResponse{TipHeight: 123, LocalHeight: 123, State: SyncState_DONE}, nil
}
func (tariFixture) GetVersion(context.Context, *emptypb.Empty) (*BaseNodeGetVersionResponse, error) {
	return &BaseNodeGetVersionResponse{Version: "6.0.1-pre.0"}, nil
}
func (tariFixture) GetNetworkState(context.Context, *GetNetworkStateRequest) (*GetNetworkStateResponse, error) {
	return &GetNetworkStateResponse{NumConnections: 4}, nil
}
func (tariFixture) GetMempoolStats(context.Context, *emptypb.Empty) (*MempoolStatsResponse, error) {
	return &MempoolStatsResponse{UnconfirmedTxs: 2, UnconfirmedWeight: 100}, nil
}

func TestTariCanonicalGRPCStatusAndFreshSyncMarker(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	fixture := tariFixture{timestamp: uint64(time.Now().Unix())}
	RegisterBaseNodeServer(server, fixture)
	go server.Serve(listener)
	defer server.Stop()
	dir := t.TempDir()
	node := nodeConfig{ID: nodeTari, Name: "Tari", Enabled: true, Supported: true, Protocol: "tari-grpc", RPCURL: "http://" + listener.Addr().String(), DataDir: dir}
	a := &app{cfg: config{stateDir: dir}}
	status, err := a.fetchTariStatus(t.Context(), node, genericNodeStatus{Enabled: true, Active: true, Supported: true})
	if err != nil || !status.Available || !status.Synced || !status.Pruned || !status.PrunedAvailable || status.Height != 123 || status.Version != "6.0.1-pre.0" || status.Connections != 4 || !status.ConnectionsAvailable || status.MempoolTxs != 2 || !status.MempoolTxsAvailable || status.MempoolBytesAvailable {
		t.Fatalf("Tari status = %#v, %v", status, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "tari.synced")); err != nil || string(b) != "ready\n" {
		t.Fatalf("sync marker = %q, %v", b, err)
	}
}

func TestTariStaleTipAndDeactivationRemoveSyncMarker(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	RegisterBaseNodeServer(server, tariFixture{timestamp: uint64(time.Now().Add(-tariTipFreshFor - time.Minute).Unix())})
	go server.Serve(listener)
	defer server.Stop()
	dir := t.TempDir()
	marker := filepath.Join(dir, "tari.synced")
	if err := os.WriteFile(marker, []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	node := nodeConfig{ID: nodeTari, Name: "Tari", Enabled: true, Supported: true, Protocol: "tari-grpc", RPCURL: "http://" + listener.Addr().String(), DataDir: dir}
	a := &app{cfg: config{stateDir: dir, nodes: map[string]nodeConfig{nodeTari: node}}}
	status, err := a.fetchTariStatus(t.Context(), node, genericNodeStatus{Enabled: true, Active: true, Supported: true})
	if err != nil || !status.Synced || !status.LatestBlockAvailable {
		t.Fatalf("stale Tari status = %#v, %v", status, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("stale tip retained sync marker: %v", err)
	}
	if err := os.WriteFile(marker, []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node-runtime.json"), []byte(`{"tari":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if info := a.healthAdditionalNode(t.Context(), nodeTari, node); info["active"] != false {
		t.Fatalf("inactive health = %#v", info)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("inactive Tari retained sync marker: %v", err)
	}
}

func TestTariDoesNotInventConnectionsWhenNetworkRPCFails(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	RegisterBaseNodeServer(server, tariNoNetworkFixture{tariFixture{timestamp: uint64(time.Now().Unix())}})
	go server.Serve(listener)
	defer server.Stop()
	dir := t.TempDir()
	node := nodeConfig{ID: nodeTari, Enabled: true, Supported: true, RPCURL: "http://" + listener.Addr().String(), DataDir: dir}
	a := &app{cfg: config{stateDir: dir}}
	status, err := a.fetchTariStatus(t.Context(), node, genericNodeStatus{Enabled: true, Active: true, Supported: true})
	if err != nil || status.Connections != 0 || status.ConnectionsAvailable {
		t.Fatalf("Tari connections = %#v, %v", status, err)
	}
}

func TestTariDoesNotInferArchiveModeFromZeroPrunedHeight(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	RegisterBaseNodeServer(server, tariUnprunedFixture{tariFixture{timestamp: uint64(time.Now().Unix())}})
	go server.Serve(listener)
	defer server.Stop()
	dir := t.TempDir()
	node := nodeConfig{ID: nodeTari, Enabled: true, Supported: true, RPCURL: "http://" + listener.Addr().String(), DataDir: dir}
	a := &app{cfg: config{stateDir: dir}}
	status, err := a.fetchTariStatus(t.Context(), node, genericNodeStatus{Enabled: true, Active: true, Supported: true})
	if err != nil || status.Pruned || status.PrunedAvailable {
		t.Fatalf("zero-pruned-height Tari status = %#v, %v", status, err)
	}
}

func TestTariSyncProgressPreventsFalseSyncedStatus(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	RegisterBaseNodeServer(server, tariSyncingFixture{tariFixture{timestamp: uint64(time.Now().Unix())}})
	go server.Serve(listener)
	defer server.Stop()
	dir := t.TempDir()
	node := nodeConfig{ID: nodeTari, Enabled: true, Supported: true, RPCURL: "http://" + listener.Addr().String(), DataDir: dir}
	a := &app{cfg: config{stateDir: dir}}
	status, err := a.fetchTariStatus(t.Context(), node, genericNodeStatus{Enabled: true, Active: true, Supported: true})
	if err != nil || status.Synced || status.Height != 123 || status.TargetHeight != 200 || status.SyncPercent != 61.5 {
		t.Fatalf("syncing Tari status = %#v, %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tari.synced")); !os.IsNotExist(err) {
		t.Fatalf("syncing Tari retained sync marker: %v", err)
	}
}

func TestTariAtGenesisIsNeverReportedSynced(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	RegisterBaseNodeServer(server, tariGenesisFixture{tariFixture{timestamp: uint64(time.Now().Unix())}})
	go server.Serve(listener)
	defer server.Stop()
	dir := t.TempDir()
	node := nodeConfig{ID: nodeTari, Enabled: true, Supported: true, RPCURL: "http://" + listener.Addr().String(), DataDir: dir}
	a := &app{cfg: config{stateDir: dir}}
	status, err := a.fetchTariStatus(t.Context(), node, genericNodeStatus{Enabled: true, Active: true, Supported: true})
	if err != nil || status.Synced || status.Height != 0 {
		t.Fatalf("Tari at genesis reported synced: %#v, %v", status, err)
	}
}

func TestTariFutureTipDoesNotCreateSyncMarker(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	RegisterBaseNodeServer(server, tariFixture{timestamp: uint64(time.Now().Add(time.Hour).Unix())})
	go server.Serve(listener)
	defer server.Stop()
	dir := t.TempDir()
	node := nodeConfig{ID: nodeTari, Enabled: true, Supported: true, RPCURL: "http://" + listener.Addr().String(), DataDir: dir}
	a := &app{cfg: config{stateDir: dir}}
	status, err := a.fetchTariStatus(t.Context(), node, genericNodeStatus{Enabled: true, Active: true, Supported: true})
	if err != nil || !status.Synced || status.LatestBlockAvailable || status.LatestBlockTimestamp != 0 || status.LatestBlockAgeSeconds != 0 {
		t.Fatalf("future-tip Tari status = %#v, %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tari.synced")); !os.IsNotExist(err) {
		t.Fatalf("future Tari tip created sync marker: %v", err)
	}
}
