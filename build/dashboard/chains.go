package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	UTXOIndex        bool
	UTXOIndexDir     string
}

type steadyCaps struct {
	CPUs  float64
	MemMB int64
}

func parseSteadyCaps(prefix, defaultMemMB string) (steadyCaps, error) {
	cpus, err := strconv.ParseFloat(strings.TrimSpace(envOr(prefix+"_STEADY_CPUS", "1")), 64)
	if err != nil || cpus <= 0 || cpus > 1024 {
		return steadyCaps{}, errors.New(prefix + "_STEADY_CPUS must be greater than 0")
	}
	memory, err := strconv.ParseInt(strings.TrimSpace(envOr(prefix+"_STEADY_MEM_LIMIT_MB", defaultMemMB)), 10, 64)
	if err != nil || memory < 128 || memory > 1<<30 {
		return steadyCaps{}, errors.New(prefix + "_STEADY_MEM_LIMIT_MB must be between 128 and 1073741824")
	}
	return steadyCaps{cpus, memory}, nil
}

func loadAdditionalNodes(cfg *config) error {
	cfg.nodes = map[string]nodeConfig{}
	cfg.coreSteady = map[string]steadyCaps{}
	for _, id := range []string{nodeBitcoin, nodeMonero} {
		caps, err := parseSteadyCaps(strings.ToUpper(id), "2048")
		if err != nil {
			return err
		}
		cfg.coreSteady[id] = caps
	}
	specs := []nodeConfig{
		{ID: nodeTari, Supported: true, Protocol: "tari-grpc", RPCURL: "http://172.29.0.32:18142", DataDir: "/data-tari", DefaultPort: "18142", ExposureName: "GRPC"},
		{ID: nodeLitecoin, Supported: true, Protocol: "bitcoin-json-rpc", RPCURL: "http://172.29.0.33:9332", DataDir: "/data-litecoin", DefaultPort: "9332", ExposureName: "RPC"},
		{ID: nodeDogecoin, Supported: true, Protocol: "bitcoin-json-rpc", RPCURL: "http://172.29.0.34:22555", DataDir: "/data-dogecoin", DefaultPort: "22555", ExposureName: "RPC"},
		{ID: nodeBitcoinCash, Supported: true, Protocol: "bitcoin-json-rpc", RPCURL: "http://172.29.0.35:8332", DataDir: "/data-bitcoincash", DefaultPort: "8332", ExposureName: "RPC"},
		{ID: nodeZcash, Supported: true, Protocol: "zcash-json-rpc", RPCURL: "http://172.29.0.36:8232", DataDir: "/data-zcash", DefaultPort: "8232", ExposureName: "RPC"},
	}
	for _, spec := range specs {
		spec.Name = nodeDisplayName(spec.ID)
		prefix := strings.ToUpper(spec.ID)
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
		caps, err := parseSteadyCaps(prefix, "4096")
		if err != nil {
			return err
		}
		profile := strings.ToLower(strings.TrimSpace(envOr(prefix+"_RESOURCE_PROFILE", "compact")))
		if profile != "compact" && profile != "off" {
			return errors.New(prefix + "_RESOURCE_PROFILE must be compact or off")
		}
		spec.SteadyCPUs, spec.SteadyMemLimitMB, spec.ResourceProfile = caps.CPUs, caps.MemMB, profile
		if utxoChains[spec.ID] != nil {
			spec.UTXOIndex = utxoIndexFlag(prefix + "_UTXO_INDEX_ENABLED")
			spec.UTXOIndexDir = envOr(prefix+"_UTXO_INDEX_DIR", "/utxo-index-"+spec.ID)
			if spec.UTXOIndex && !filepath.IsAbs(spec.UTXOIndexDir) {
				return errors.New(prefix + "_UTXO_INDEX_DIR must be an absolute path")
			}
		}
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
	Enabled                   bool     `json:"enabled"`
	Active                    bool     `json:"active"`
	Supported                 bool     `json:"supported"`
	Available                 bool     `json:"available"`
	Synced                    bool     `json:"synced"`
	TipStale                  bool     `json:"-"`
	Height                    int64    `json:"height"`
	TargetHeight              int64    `json:"target_height"`
	SyncPercent               float64  `json:"sync_percent"`
	Version                   string   `json:"version"`
	UptimeSeconds             int64    `json:"uptime_seconds"`
	UptimeAvailable           bool     `json:"uptime_available"`
	Connections               int64    `json:"connections"`
	ConnectionsAvailable      bool     `json:"connections_available"`
	IncomingConnections       int64    `json:"incoming_connections"`
	OutgoingConnections       int64    `json:"outgoing_connections"`
	ConnectionsSplitAvailable bool     `json:"connections_split_available"`
	Pruned                    bool     `json:"pruned"`
	PrunedAvailable           bool     `json:"pruned_available"`
	DatabaseSizeBytes         int64    `json:"database_size_bytes"`
	DatabaseSizeAvailable     bool     `json:"database_size_available"`
	MempoolTxs                int64    `json:"mempool_txs"`
	MempoolTxsAvailable       bool     `json:"mempool_txs_available"`
	MempoolBytes              int64    `json:"mempool_bytes"`
	MempoolBytesAvailable     bool     `json:"mempool_bytes_available"`
	LatestBlockTimestamp      int64    `json:"latest_block_timestamp"`
	LatestBlockAgeSeconds     int64    `json:"latest_block_age_seconds"`
	LatestBlockAvailable      bool     `json:"latest_block_available"`
	FeeEstimates              []string `json:"fee_estimates,omitempty"`
	FeeUnit                   string   `json:"fee_unit,omitempty"`
	FeesAvailable             bool     `json:"fees_available"`
	DiskTotalBytes            uint64   `json:"disk_total_bytes"`
	DiskFreeBytes             uint64   `json:"disk_free_bytes"`
	DiskAvailable             bool     `json:"disk_available"`
	RPCExposureEnabled        bool     `json:"rpc_exposure_enabled"`
	RPCEndpoint               string   `json:"rpc_endpoint"`
	WalletViewSupported       bool     `json:"wallet_view_supported"`
	WalletViewReason          string   `json:"wallet_view_reason,omitempty"`
	SteadyCPUs                float64  `json:"steady_cpus"`
	SteadyMemLimitMB          int64    `json:"steady_mem_limit_mb"`
	ResourceProfile           string   `json:"resource_profile"`
	Error                     string   `json:"error"`
	UpdatedAt                 int64    `json:"updated_at"`
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
	SizeOnDisk           int64   `json:"size_on_disk"`
}

// Zakura's estimatedheight runs ahead of a synced tip once the newest block is 75 s old, so only headers bound the tip.
func (c zcashBlockchainInfo) atTip() bool {
	return c.BestBlockHash != "" && c.Blocks >= c.Headers && c.VerificationProgress >= 0.999999
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
	status.WalletViewSupported, status.WalletViewReason = a.familyWatchCapability(id)
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
	rpc := &rpcClient{url: node.RPCURL, cookiePath: node.RPCCookiePath, client: newInternalHTTPClient(5 * time.Second)}
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
	status.Pruned, status.PrunedAvailable = true, true
	status.DatabaseSizeBytes, status.DatabaseSizeAvailable = chain.SizeOnDisk, chain.SizeOnDisk > 0
	var network networkInfo
	if rpc.call(ctx, "getnetworkinfo", nil, &network) == nil {
		status.Version, status.Connections, status.ConnectionsAvailable = network.Subversion, network.Connections, true
	}
	var peers []peerInfo
	if rpc.call(ctx, "getpeerinfo", nil, &peers) == nil {
		status.ConnectionsAvailable = true
		status.ConnectionsSplitAvailable = true
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
	status.Synced = chain.atTip() && status.LatestBlockAvailable && status.LatestBlockAgeSeconds <= int64((30*time.Minute)/time.Second)
	if status.Synced {
		status.SyncPercent, status.TargetHeight = 100, status.Height
	}
	var fee zcashStandardFee
	if rpc.call(ctx, "getstandardfee", nil, &fee) == nil && fee.StandardFee > 0 {
		value := strconv.FormatInt(fee.StandardFee, 10)
		status.FeeEstimates = []string{value}
		status.FeeUnit, status.FeesAvailable = "zats/action", true
	}
	if disk := diskUsage(node.DataDir); disk.Total > 0 {
		status.DiskTotalBytes, status.DiskFreeBytes, status.DiskAvailable = disk.Total, disk.Free, true
	}
	a.fillFromHostStats(node.ID, &status)
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
	rpc := &rpcClient{url: node.RPCURL, user: node.RPCUser, password: node.RPCPassword, client: newInternalHTTPClient(5 * time.Second)}
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
	tipTime := a.chainTipTime(ctx, node.ID, rpc, chain.BestBlockHash)
	behind := chainBehind(chain.InitialBlockDownload, chain.Blocks, chain.Headers, tipTime, time.Now())
	status.Synced = !chain.InitialBlockDownload && status.Height >= status.TargetHeight && !chainTipStale(tipTime, time.Now())
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
		status.ConnectionsSplitAvailable = true
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
	if status.ConnectionsAvailable {
		a.noteNodeNetwork(node.ID, status.Version, status.Connections)
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
	if tipTime > 0 {
		status.LatestBlockTimestamp, status.LatestBlockAvailable = tipTime, true
		status.LatestBlockAgeSeconds = max(0, time.Now().Unix()-tipTime)
	}
	targets := []int{1, 3, 6}
	unit := "litoshi/vB"
	if node.ID == nodeBitcoinCash {
		targets, unit = []int{1}, "sat/B"
	} else if node.ID == nodeDogecoin {
		unit = "DOGE/kB"
	}
	for _, blocks := range targets {
		if behind {
			break
		}
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
	a.fillFromHostStats(node.ID, &status)
	writeJSON(w, status)
}

type chainView struct {
	ID           string
	Label        string
	Accent       string
	PriceAsset   string
	RPCName      string
	AlertsPerDay int
	Family       bool
	GRPC         bool
	Templated    bool
	UTXOIndex    bool
	Unavailable  bool
}

type chainPage struct {
	label        string
	accent       string
	priceAsset   string
	rpcName      string
	alertsPerDay int
	family       bool
	grpc         bool
	unavailable  bool
}

var chainPages = map[string]chainPage{
	nodeBitcoin:     {label: "Bitcoin", accent: "#f2a900", priceAsset: "bitcoin", alertsPerDay: 144},
	nodeMonero:      {label: "Monero", accent: "#ff6600", priceAsset: "monero", alertsPerDay: 720},
	nodeTari:        {label: "Tari", accent: "#8f5cff", priceAsset: "tari", alertsPerDay: 720, rpcName: "gRPC", grpc: true},
	nodeLitecoin:    {label: "Litecoin", accent: "#4d8fd8", priceAsset: "litecoin", alertsPerDay: 576, family: true, rpcName: "RPC"},
	nodeDogecoin:    {label: "Dogecoin", accent: "#b88746", priceAsset: "dogecoin", alertsPerDay: 1440, family: true, rpcName: "RPC"},
	nodeBitcoinCash: {label: "Bitcoin Cash", accent: "#36a852", priceAsset: "bitcoin-cash", alertsPerDay: 144, family: true, rpcName: "RPC"},
	nodeZcash:       {label: "Zcash", accent: "#d69c35", priceAsset: "zcash", alertsPerDay: 1152, family: true, rpcName: "read-only RPC", unavailable: true},
}

func chainViewFor(id string) chainView {
	page := chainPages[id]
	return chainView{
		ID: id, Label: nodeDisplayName(id), Accent: page.accent, PriceAsset: page.priceAsset, RPCName: page.rpcName,
		AlertsPerDay: page.alertsPerDay, Family: page.family, GRPC: page.grpc, Templated: id != nodeBitcoin && id != nodeMonero, UTXOIndex: utxoChains[id] != nil, Unavailable: page.unavailable,
	}
}

var dashboardChains = func() []chainView {
	chains := make([]chainView, 0, len(nodeOrder))
	for _, id := range nodeOrder {
		chains = append(chains, chainViewFor(id))
	}
	return chains
}()
