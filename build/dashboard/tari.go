package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

const tariTipFreshFor = 30 * time.Minute

func (a *app) writeTariStatus(w http.ResponseWriter, r *http.Request, node nodeConfig, status genericNodeStatus) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	current, err := a.fetchTariStatus(ctx, node, status)
	if err != nil {
		a.recordNodeHealth(node.ID, false)
		a.updateTariSyncMarker(false)
		current.Error = "Tari gRPC unavailable"
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	writeJSON(w, current)
}

func (a *app) fetchTariStatus(ctx context.Context, node nodeConfig, status genericNodeStatus) (genericNodeStatus, error) {
	endpoint, err := url.Parse(node.RPCURL)
	if err != nil || endpoint.Host == "" {
		return status, errors.New("invalid Tari gRPC endpoint")
	}
	connection, err := grpc.NewClient(endpoint.Host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return status, err
	}
	defer connection.Close()
	client := NewBaseNodeClient(connection)
	tip, err := client.GetTipInfo(ctx, &emptypb.Empty{})
	if err != nil || tip.Metadata == nil {
		return status, errors.New("Tari tip unavailable")
	}
	progress, progressErr := client.GetSyncProgress(ctx, &emptypb.Empty{})
	status.Available, status.UpdatedAt = true, time.Now().Unix()
	a.recordNodeHealth(node.ID, true)
	status.Height = int64(tip.Metadata.BestBlockHeight)
	status.TargetHeight = status.Height
	if progressErr == nil {
		status.Height, status.TargetHeight = int64(progress.LocalHeight), int64(progress.TipHeight)
		if status.TargetHeight < status.Height {
			status.TargetHeight = status.Height
		}
	}
	status.Synced = tip.InitialSyncAchieved && tip.BaseNodeState == BaseNodeState_LISTENING && status.Height > 0
	if progressErr == nil {
		status.Synced = status.Synced && progress.State == SyncState_DONE && progress.LocalHeight >= progress.TipHeight
	}
	if status.Synced {
		status.SyncPercent = 100
	} else if status.TargetHeight > 0 {
		status.SyncPercent = min(100, float64(status.Height)/float64(status.TargetHeight)*100)
	}
	status.Pruned = tip.Metadata.PrunedHeight > 0
	status.PrunedAvailable = status.Pruned
	now := time.Now().Unix()
	if tip.Metadata.Timestamp > 0 && tip.Metadata.Timestamp <= uint64(now) {
		status.LatestBlockTimestamp, status.LatestBlockAvailable = int64(tip.Metadata.Timestamp), true
		status.LatestBlockAgeSeconds = now - int64(tip.Metadata.Timestamp)
	}
	if version, versionErr := client.GetVersion(ctx, &emptypb.Empty{}); versionErr == nil {
		status.Version = version.Version
	}
	if network, networkErr := client.GetNetworkState(ctx, &GetNetworkStateRequest{}); networkErr == nil {
		status.Connections = int64(network.NumConnections)
		status.ConnectionsAvailable = true
	}
	if mempool, mempoolErr := client.GetMempoolStats(ctx, &emptypb.Empty{}); mempoolErr == nil {
		status.MempoolTxs, status.MempoolTxsAvailable = int64(mempool.UnconfirmedTxs), true
	}
	if disk := diskUsage(node.DataDir); disk.Total > 0 {
		status.DiskTotalBytes, status.DiskFreeBytes, status.DiskAvailable = disk.Total, disk.Free, true
	}
	tipFresh := status.LatestBlockAvailable && status.LatestBlockAgeSeconds <= int64(tariTipFreshFor/time.Second)
	a.updateTariSyncMarker(status.Synced && tipFresh)
	return status, nil
}

func (a *app) updateTariSyncMarker(synced bool) {
	path := filepath.Join(a.cfg.stateDir, "tari.synced")
	if !synced {
		_ = os.Remove(path)
		return
	}
	_ = writePrivateFileAtomic(path, []byte("ready\n"))
}
