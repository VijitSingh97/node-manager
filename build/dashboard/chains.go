package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	nodeBitcoin     = "bitcoin"
	nodeMonero      = "monero"
	nodeTari        = "tari"
	nodeLitecoin    = "litecoin"
	nodeDogecoin    = "dogecoin"
	nodeBitcoinCash = "bitcoincash"
	nodeZcash       = "zcash"
)

var nodeOrder = []string{nodeBitcoin, nodeMonero, nodeTari, nodeLitecoin, nodeDogecoin, nodeBitcoinCash, nodeZcash}

type nodeConfig struct {
	ID               string
	Name             string
	Enabled          bool
	Active           bool
	ActiveSet        bool
	Supported        bool
	Protocol         string
	RPCURL           string
	RPCUser          string
	RPCPassword      string
	RPCCookiePath    string
	DataDir          string
	RPCEndpoint      string
	DefaultPort      string
	ExposureName     string
	SteadyCPUs       float64
	SteadyMemLimitMB int64
	ResourceProfile  string
}

func loadAdditionalNodes(cfg *config) error {
	cfg.nodes = map[string]nodeConfig{}
	specs := []nodeConfig{
		{ID: nodeTari, Name: "Tari", Supported: true, Protocol: "tari-grpc", RPCURL: "http://172.29.0.32:18142", DataDir: "/data-tari", DefaultPort: "18142", ExposureName: "GRPC"},
		{ID: nodeLitecoin, Name: "Litecoin", Supported: true, Protocol: "bitcoin-json-rpc", RPCURL: "http://172.29.0.33:9332", DataDir: "/data-litecoin", DefaultPort: "9332", ExposureName: "RPC"},
		{ID: nodeDogecoin, Name: "Dogecoin", Supported: true, Protocol: "bitcoin-json-rpc", RPCURL: "http://172.29.0.34:22555", DataDir: "/data-dogecoin", DefaultPort: "22555", ExposureName: "RPC"},
		{ID: nodeBitcoinCash, Name: "Bitcoin Cash", Supported: true, Protocol: "bitcoin-json-rpc", RPCURL: "http://172.29.0.35:8332", DataDir: "/data-bitcoincash", DefaultPort: "8332", ExposureName: "RPC"},
		{ID: nodeZcash, Name: "Zcash", Supported: true, Protocol: "zcash-json-rpc", RPCURL: "http://172.29.0.36:8232", DataDir: "/data-zcash", DefaultPort: "8232", ExposureName: "RPC"},
	}
	for _, spec := range specs {
		prefix := strings.ToUpper(spec.ID)
		if spec.ID == nodeBitcoinCash {
			prefix = "BITCOINCASH"
		}
		spec.Enabled = enabled(os.Getenv(prefix + "_ENABLED"))
		spec.Active = !explicitlyDisabled(os.Getenv(prefix + "_ACTIVE"))
		spec.ActiveSet = true
		urlName := prefix + "_RPC_URL"
		if spec.Protocol == "tari-grpc" {
			urlName = prefix + "_GRPC_URL"
		}
		spec.RPCURL = strings.TrimSpace(envOr(urlName, spec.RPCURL))
		spec.DataDir = envOr(prefix+"_DATA_DIR", spec.DataDir)
		spec.RPCUser, spec.RPCPassword = os.Getenv(prefix+"_RPC_USER"), os.Getenv(prefix+"_RPC_PASSWORD")
		if spec.ID == nodeZcash {
			spec.RPCCookiePath = envOr("ZCASH_RPC_COOKIE_PATH", spec.DataDir+"/.cookie")
		}
		cpus, err := strconv.ParseFloat(strings.TrimSpace(envOr(prefix+"_STEADY_CPUS", "1")), 64)
		if err != nil || cpus <= 0 || cpus > 1024 {
			return errors.New(prefix + "_STEADY_CPUS must be greater than 0")
		}
		memory, err := strconv.ParseInt(strings.TrimSpace(envOr(prefix+"_STEADY_MEM_LIMIT_MB", "4096")), 10, 64)
		if err != nil || memory < 128 || memory > 1<<30 {
			return errors.New(prefix + "_STEADY_MEM_LIMIT_MB must be between 128 and 1073741824")
		}
		profile := strings.ToLower(strings.TrimSpace(envOr(prefix+"_RESOURCE_PROFILE", "compact")))
		if profile != "compact" && profile != "off" {
			return errors.New(prefix + "_RESOURCE_PROFILE must be compact or off")
		}
		spec.SteadyCPUs, spec.SteadyMemLimitMB, spec.ResourceProfile = cpus, memory, profile
		if err := validateInternalHTTPURL(urlName, spec.RPCURL); err != nil {
			return err
		}
		if spec.Enabled {
			spec.RPCEndpoint, err = exposedLANEndpoint(prefix+"_"+spec.ExposureName+"_EXPOSE", prefix+"_"+spec.ExposureName+"_BIND_IP", prefix+"_"+spec.ExposureName+"_PORT", spec.DefaultPort)
			if err != nil {
				return err
			}
			if spec.Protocol == "bitcoin-json-rpc" && (spec.RPCUser == "" || spec.RPCPassword == "") {
				return errors.New(prefix + "_RPC_USER and " + prefix + "_RPC_PASSWORD are required when " + prefix + "_ENABLED is true")
			}
			if spec.ID == nodeZcash && spec.RPCEndpoint != "" && (spec.RPCUser == "" || spec.RPCPassword == "") {
				return errors.New("ZCASH_RPC_USER and ZCASH_RPC_PASSWORD are required when ZCASH_RPC_EXPOSE is true")
			}
		}
		cfg.nodes[spec.ID] = spec
	}
	return nil
}

func validateInternalHTTPURL(name, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || !internalNodeHost(parsed.Hostname()) {
		return errors.New(name + " must be an internal HTTP URL without credentials, path, query, or fragment")
	}
	return nil
}

func internalNodeHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" {
		return true
	}
	if address := net.ParseIP(host); address != nil {
		return address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast()
	}
	return host != "" && (!strings.Contains(host, ".") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".home.arpa"))
}

type genericNodeStatus struct {
	Enabled               bool     `json:"enabled"`
	Active                bool     `json:"active"`
	Supported             bool     `json:"supported"`
	Available             bool     `json:"available"`
	Synced                bool     `json:"synced"`
	Height                int64    `json:"height"`
	TargetHeight          int64    `json:"target_height"`
	SyncPercent           float64  `json:"sync_percent"`
	Version               string   `json:"version"`
	UptimeSeconds         int64    `json:"uptime_seconds"`
	UptimeAvailable       bool     `json:"uptime_available"`
	Connections           int64    `json:"connections"`
	ConnectionsAvailable  bool     `json:"connections_available"`
	IncomingConnections   int64    `json:"incoming_connections"`
	OutgoingConnections   int64    `json:"outgoing_connections"`
	Pruned                bool     `json:"pruned"`
	PrunedAvailable       bool     `json:"pruned_available"`
	DatabaseSizeBytes     int64    `json:"database_size_bytes"`
	DatabaseSizeAvailable bool     `json:"database_size_available"`
	MempoolTxs            int64    `json:"mempool_txs"`
	MempoolTxsAvailable   bool     `json:"mempool_txs_available"`
	MempoolBytes          int64    `json:"mempool_bytes"`
	MempoolBytesAvailable bool     `json:"mempool_bytes_available"`
	LatestBlockTimestamp  int64    `json:"latest_block_timestamp"`
	LatestBlockAgeSeconds int64    `json:"latest_block_age_seconds"`
	LatestBlockAvailable  bool     `json:"latest_block_available"`
	EndOfSupportHeight    int64    `json:"end_of_support_height,omitempty"`
	EndOfSupportRemaining int64    `json:"end_of_support_remaining_blocks,omitempty"`
	EndOfSupportWarning   bool     `json:"end_of_support_warning,omitempty"`
	FeeEstimates          []string `json:"fee_estimates,omitempty"`
	FeeUnit               string   `json:"fee_unit,omitempty"`
	FeesAvailable         bool     `json:"fees_available"`
	DiskTotalBytes        uint64   `json:"disk_total_bytes"`
	DiskFreeBytes         uint64   `json:"disk_free_bytes"`
	DiskAvailable         bool     `json:"disk_available"`
	RPCExposureEnabled    bool     `json:"rpc_exposure_enabled"`
	RPCEndpoint           string   `json:"rpc_endpoint"`
	WalletViewSupported   bool     `json:"wallet_view_supported"`
	WalletViewReason      string   `json:"wallet_view_reason,omitempty"`
	SteadyCPUs            float64  `json:"steady_cpus"`
	SteadyMemLimitMB      int64    `json:"steady_mem_limit_mb"`
	ResourceProfile       string   `json:"resource_profile"`
	Error                 string   `json:"error"`
	UpdatedAt             int64    `json:"updated_at"`
}

type familyBlockchainInfo struct {
	Blocks               int64   `json:"blocks"`
	Headers              int64   `json:"headers"`
	BestBlockHash        string  `json:"bestblockhash"`
	VerificationProgress float64 `json:"verificationprogress"`
	InitialBlockDownload bool    `json:"initialblockdownload"`
	Pruned               bool    `json:"pruned"`
	SizeOnDisk           int64   `json:"size_on_disk"`
}

type familyBlockHeader struct {
	Time int64 `json:"time"`
}

type zcashBlockchainInfo struct {
	Blocks               int64   `json:"blocks"`
	Headers              int64   `json:"headers"`
	EstimatedHeight      int64   `json:"estimatedheight"`
	BestBlockHash        string  `json:"bestblockhash"`
	VerificationProgress float64 `json:"verificationprogress"`
	Pruned               bool    `json:"pruned"`
	SizeOnDisk           int64   `json:"size_on_disk"`
}

type zcashStandardFee struct {
	StandardFee int64 `json:"standard_fee"`
}

func (a *app) additionalNode(id string) (nodeConfig, bool) {
	node, ok := a.cfg.nodes[id]
	return node, ok
}

func (a *app) apiAdditionalNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("node")
	node, ok := a.additionalNode(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	active := a.nodeActive(id, node.Enabled)
	status := genericNodeStatus{
		Enabled: node.Enabled, Active: active, Supported: node.Supported,
		RPCExposureEnabled: node.RPCEndpoint != "", RPCEndpoint: node.RPCEndpoint,
		WalletViewSupported: false, WalletViewReason: "Watch-only wallet view is not available for this node.",
		SteadyCPUs: node.SteadyCPUs, SteadyMemLimitMB: node.SteadyMemLimitMB, ResourceProfile: node.ResourceProfile,
	}
	status.WalletViewSupported, status.WalletViewReason = familyWatchCapability(id)
	if id == nodeTari {
		status.WalletViewSupported = a.cfg.tariWalletEnabled
		status.WalletViewReason = ""
		if !status.WalletViewSupported {
			status.WalletViewReason = "Apply the updated stack to enable the Tari view-only wallet service."
		}
	}
	if !node.Supported {
		status.Error = "Unavailable until pruned node support exists"
		writeJSON(w, status)
		return
	}
	if !node.Enabled || !active {
		a.recordNodeHealth(id, false)
		if id == nodeTari {
			a.updateTariSyncMarker(false)
		}
		writeJSON(w, status)
		return
	}
	if node.Protocol == "tari-grpc" {
		a.writeTariStatus(w, r, node, status)
		return
	}
	if node.Protocol == "zcash-json-rpc" {
		a.writeZcashStatus(w, r, node, status)
		return
	}
	a.writeBitcoinFamilyStatus(w, r, node, status)
}

func (a *app) fetchZcashStatus(ctx context.Context, node nodeConfig, status genericNodeStatus) (genericNodeStatus, error) {
	rpc := &rpcClient{url: node.RPCURL, cookiePath: node.RPCCookiePath, client: &http.Client{Timeout: 5 * time.Second}}
	var chain zcashBlockchainInfo
	if err := rpc.call(ctx, "getblockchaininfo", nil, &chain); err != nil {
		return status, err
	}
	status.Available, status.UpdatedAt = true, time.Now().Unix()
	status.Height = chain.Blocks
	status.TargetHeight = max(chain.Blocks, max(chain.Headers, chain.EstimatedHeight))
	if chain.VerificationProgress > 0 {
		status.SyncPercent = min(100, chain.VerificationProgress*100)
	} else if status.TargetHeight > 0 {
		status.SyncPercent = min(100, float64(status.Height)/float64(status.TargetHeight)*100)
	}
	status.Pruned, status.PrunedAvailable = chain.Pruned, true
	status.DatabaseSizeBytes, status.DatabaseSizeAvailable = chain.SizeOnDisk, chain.SizeOnDisk > 0
	var network networkInfo
	if rpc.call(ctx, "getnetworkinfo", nil, &network) == nil {
		status.Version, status.Connections, status.ConnectionsAvailable = network.Subversion, network.Connections, true
	}
	var peers []peerInfo
	if rpc.call(ctx, "getpeerinfo", nil, &peers) == nil {
		status.ConnectionsAvailable = true
		for _, peer := range peers {
			if peer.Inbound {
				status.IncomingConnections++
			} else {
				status.OutgoingConnections++
			}
		}
		if status.Connections == 0 {
			status.Connections = int64(len(peers))
		}
	}
	var mempool mempoolInfo
	if rpc.call(ctx, "getmempoolinfo", nil, &mempool) == nil {
		status.MempoolTxs, status.MempoolBytes = mempool.Size, mempool.Bytes
		status.MempoolTxsAvailable, status.MempoolBytesAvailable = true, true
	}
	if chain.BestBlockHash != "" {
		var header familyBlockHeader
		if rpc.call(ctx, "getblockheader", []any{chain.BestBlockHash}, &header) == nil && header.Time > 0 {
			status.LatestBlockTimestamp, status.LatestBlockAvailable = header.Time, true
			status.LatestBlockAgeSeconds = max(0, time.Now().Unix()-header.Time)
		}
	}
	status.Synced = status.Height >= status.TargetHeight && chain.VerificationProgress >= 0.999999 && status.LatestBlockAvailable && status.LatestBlockAgeSeconds <= int64((30*time.Minute)/time.Second)
	if status.Synced {
		status.SyncPercent = 100
	}
	var fee zcashStandardFee
	if rpc.call(ctx, "getstandardfee", nil, &fee) == nil && fee.StandardFee > 0 {
		value := strconv.FormatInt(fee.StandardFee, 10)
		status.FeeEstimates = []string{value}
		status.FeeUnit, status.FeesAvailable = "zats/action", true
	}
	applyZakuraEndOfSupport(ctx, rpc, &status)
	if disk := diskUsage(node.DataDir); disk.Total > 0 {
		status.DiskTotalBytes, status.DiskFreeBytes, status.DiskAvailable = disk.Total, disk.Free, true
	}
	return status, nil
}

func (a *app) writeZcashStatus(w http.ResponseWriter, r *http.Request, node nodeConfig, status genericNodeStatus) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	status, err := a.fetchZcashStatus(ctx, node, status)
	if err != nil {
		a.recordNodeHealth(node.ID, false)
		status.Error = node.Name + " RPC unavailable"
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, status)
		return
	}
	a.recordNodeHealth(node.ID, true)
	writeJSON(w, status)
}

func (a *app) writeBitcoinFamilyStatus(w http.ResponseWriter, r *http.Request, node nodeConfig, status genericNodeStatus) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rpc := &rpcClient{url: node.RPCURL, user: node.RPCUser, password: node.RPCPassword, client: &http.Client{Timeout: 5 * time.Second}}
	var chain familyBlockchainInfo
	if rpc.call(ctx, "getblockchaininfo", nil, &chain) != nil {
		a.recordNodeHealth(node.ID, false)
		status.Error = node.Name + " RPC unavailable"
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, status)
		return
	}
	status.Available, status.UpdatedAt = true, time.Now().Unix()
	a.recordNodeHealth(node.ID, true)
	status.Height, status.TargetHeight = chain.Blocks, chain.Headers
	if status.TargetHeight < status.Height {
		status.TargetHeight = status.Height
	}
	status.Synced = !chain.InitialBlockDownload && status.Height >= status.TargetHeight
	if status.Synced {
		status.SyncPercent = 100
	} else if chain.VerificationProgress > 0 {
		status.SyncPercent = min(100, chain.VerificationProgress*100)
	} else if status.TargetHeight > 0 {
		status.SyncPercent = min(100, float64(status.Height)/float64(status.TargetHeight)*100)
	}
	status.Pruned, status.PrunedAvailable, status.DatabaseSizeBytes = chain.Pruned, true, chain.SizeOnDisk
	status.DatabaseSizeAvailable = chain.SizeOnDisk > 0
	var network networkInfo
	if rpc.call(ctx, "getnetworkinfo", nil, &network) == nil {
		status.Version, status.Connections = network.Subversion, network.Connections
		status.ConnectionsAvailable = true
	}
	var peers []peerInfo
	if rpc.call(ctx, "getpeerinfo", nil, &peers) == nil {
		status.ConnectionsAvailable = true
		for _, peer := range peers {
			if peer.Inbound {
				status.IncomingConnections++
			} else {
				status.OutgoingConnections++
			}
		}
		if status.Connections == 0 {
			status.Connections = int64(len(peers))
		}
	}
	var uptime float64
	if rpc.call(ctx, "uptime", nil, &uptime) == nil && uptime >= 0 {
		status.UptimeSeconds, status.UptimeAvailable = int64(uptime), true
	}
	var mempool mempoolInfo
	if rpc.call(ctx, "getmempoolinfo", nil, &mempool) == nil {
		status.MempoolTxs, status.MempoolBytes = mempool.Size, mempool.Bytes
		status.MempoolTxsAvailable, status.MempoolBytesAvailable = true, true
	}
	if chain.BestBlockHash != "" {
		var header familyBlockHeader
		if rpc.call(ctx, "getblockheader", []any{chain.BestBlockHash}, &header) == nil && header.Time > 0 {
			status.LatestBlockTimestamp, status.LatestBlockAvailable = header.Time, true
			status.LatestBlockAgeSeconds = max(0, time.Now().Unix()-header.Time)
		}
	}
	targets := []int{1, 3, 6}
	unit := "litoshi/vB"
	if node.ID == nodeBitcoinCash {
		targets, unit = []int{1}, "sat/B"
	} else if node.ID == nodeDogecoin {
		unit = "DOGE/kB"
	}
	for _, blocks := range targets {
		rate, ok := familyFeeRate(ctx, rpc, node.ID, blocks)
		if !ok {
			status.FeeEstimates = nil
			break
		}
		status.FeeEstimates = append(status.FeeEstimates, formatFamilyFeeRate(node.ID, rate))
	}
	status.FeesAvailable = len(status.FeeEstimates) == len(targets)
	if status.FeesAvailable {
		status.FeeUnit = unit
	}
	if disk := diskUsage(node.DataDir); disk.Total > 0 {
		status.DiskTotalBytes, status.DiskFreeBytes, status.DiskAvailable = disk.Total, disk.Free, true
	}
	writeJSON(w, status)
}
