package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const maxFees = 1440

type blockchainInfo struct {
	Blocks               int64   `json:"blocks"`
	Headers              int64   `json:"headers"`
	BestBlockHash        string  `json:"bestblockhash"`
	VerificationProgress float64 `json:"verificationprogress"`
	InitialBlockDownload bool    `json:"initialblockdownload"`
	Pruned               bool    `json:"pruned"`
	SizeOnDisk           int64   `json:"size_on_disk"`
	Chain                string  `json:"chain"`
	PruneHeight          int64   `json:"pruneheight"`
}

type networkInfo struct {
	Subversion  string `json:"subversion"`
	Connections int64  `json:"connections"`
}

type peerInfo struct {
	Inbound bool `json:"inbound"`
}

type mempoolInfo struct {
	Size  int64 `json:"size"`
	Bytes int64 `json:"bytes"`
}

type feeEstimate struct {
	FeeRate *float64 `json:"feerate"`
}

type diskInfo struct {
	Total uint64
	Free  uint64
}

type pageData struct {
	NodeName             string
	Command              string
	PasswordEnabled      bool
	BitcoinEnabled       bool
	Loading              bool
	LastKnownSync        bool
	LastSyncTime         string
	InitialBlockDownload bool
	Version              string
	UpdateNote           string
	CoreVersion          string
	Uptime               string
	Blocks               int64
	Headers              int64
	Progress             float64
	PeersUnknown         bool
	Inbound              int
	Outbound             int
	Pruned               bool
	NodeGB               float64
	NodeSizeBytes        int64
	ShowMempool          bool
	MempoolTxs           int64
	MempoolMB            float64
	FeeNext              string
	Fee30m               string
	FeeHour              string
	NextBlock            int64
	LastUpdate           string
	UpdatedAt            int64
	StateLine            string
	LastBlock            string
	BitcoinRPCEndpoint   string
	Chains               []chainView
}

type app struct {
	cfg              config
	rpc              *rpcClient
	template         *template.Template
	static           http.Handler
	feesMu           sync.RWMutex
	fees             []float64
	additionalFeesMu sync.RWMutex
	additionalFees   map[string][]float64
	nodeHealthMu     sync.RWMutex
	nodeHealth       map[string]nodeHealthSnapshot
	nodeInfo         map[string]nodeInfoSnapshot
	syncMu           sync.RWMutex
	lastSync         *syncSnapshot
	log              *log.Logger
	watch            *WatchStore
	monitor          *Monitor
	prices           *PriceCache
	scanner          *prunedScanner
	moneroWatch      *MoneroWatchStore
	moneroFees       *MoneroFeeHistory
	familyWatch      *familyWatchManager
	tariWatch        *TariWatchStore
	utxo             *utxoManager
	chainUtxo        map[string]*utxoManager
	utxoSeedLock     sync.Mutex
	watchWake        chan struct{}
	dashboardAuth    *dashboardAuth
	sessions         *dashboardSessions
	loginLimiter     *loginLimiter
	passwordWork     *passwordWorkLimiter
	passwordAttempts chan struct{}
	controlMu        sync.Mutex
	provisionLocks   sync.Map
	ctx              context.Context
}

type syncSnapshot struct {
	blocks   int64
	headers  int64
	progress float64
	time     time.Time
}

func newApp(cfg config, rpc *rpcClient, templatePath, staticPath string, logger *log.Logger) (*app, error) {
	if err := removeLegacyUpgradeRequest(cfg.stateDir); err != nil {
		return nil, err
	}
	t, err := template.ParseFiles(templatePath, filepath.Join(filepath.Dir(templatePath), "login.html"))
	if err != nil {
		return nil, err
	}
	a := &app{cfg: cfg, rpc: rpc, template: t, static: http.StripPrefix("/static/", http.FileServer(http.Dir(staticPath))), log: logger, additionalFees: make(map[string][]float64), nodeHealth: make(map[string]nodeHealthSnapshot)}
	a.sessions = newDashboardSessions()
	a.loginLimiter = newLoginLimiter()
	a.passwordWork = newPasswordWorkLimiter()
	a.passwordAttempts = make(chan struct{}, 2)
	a.dashboardAuth, err = newDashboardAuth(cfg.stateDir, cfg.dashboardPassword)
	if err != nil {
		return nil, err
	}
	if !a.dashboardAuth.enabled {
		return nil, errors.New("dashboard sign-in requires a password; set dashboard.password in config.json before starting")
	}
	if cfg.stateDir != "" {
		a.sessions.persist(filepath.Join(cfg.stateDir, "sessions.json"), a.dashboardAuth.sessionBinding(), time.Now())
	}
	removeLegacyActuatorCredentials(cfg.stateDir, a.log)
	a.watch = NewWatchStore(cfg.stateDir)
	if a.watch.ScanErr() != nil {
		a.log.Print("watch balance cache unavailable; pruned balance scans disabled until the cache is repaired and the dashboard restarts")
	}
	a.scanner = newPrunedScanner(a)
	a.monitor = NewMonitor(cfg.version, cfg.dataDir, cfg.stateDir)
	a.monitor.configureNodes(!cfg.bitcoinDisabled, cfg.moneroEnabled, cfg.moneroDataDir)
	a.monitor.configureAdditionalNodes(cfg.nodes)
	a.monitor.transitioning = a.monitorTransitioning
	a.monitor.restarting = a.nodeRestarting
	if a.monitor.settingsErr != nil {
		a.log.Print("notification settings unavailable; alerts disabled until the state file is repaired and the dashboard restarts")
	}
	a.prices = NewPriceCache()
	if cfg.stateDir != "" {
		a.prices.Persist(filepath.Join(cfg.stateDir, "prices.json"))
	}
	a.moneroWatch = NewMoneroWatchStore(cfg)
	a.moneroFees = NewMoneroFeeHistory(cfg.stateDir)
	a.familyWatch = newFamilyWatchManager(a)
	a.tariWatch = NewTariWatchStore(cfg)
	if cfg.utxoIndexEnabled && !cfg.bitcoinDisabled {
		blockRPC := *rpc
		blockRPC.client, blockRPC.maxBody = newInternalHTTPClient(2*time.Minute), 64<<20
		a.utxo = newUtxoManager(cfg.utxoIndexDir, nil, &blockRPC, logger, utxoBitcoin)
		a.utxo.blockWait = &blockRPC
		a.watchWake = make(chan struct{}, 1)
		a.utxo.onApplied = func() {
			select {
			case a.watchWake <- struct{}{}:
			default:
			}
		}
		if cfg.stateDir != "" {
			a.utxo.setSeedStatePath(filepath.Join(cfg.stateDir, "utxo-seed.json"))
			dumpRPC := *rpc
			dumpRPC.client, dumpRPC.maxBody = newInternalHTTPClient(0), 1<<20
			a.utxo.configureSeed(utxoSeedHost{stateDir: cfg.stateDir, dataDir: cfg.dataDir, dump: &dumpRPC, hold: a.scanner.hold, release: a.scanner.release, lock: &a.utxoSeedLock,
				autoSeed: func() (string, string) { return a.queueSeedHostAction(nodeBitcoin) }, autoResult: a.seedHostResult, otherSeedActive: a.anySeedActive})
			a.utxo.configureBench(a.benchHost())
		}
	}
	a.chainUtxo = map[string]*utxoManager{}
	for id, chain := range utxoChains {
		node := cfg.nodes[id]
		if !node.Enabled || !node.UTXOIndex {
			continue
		}
		if clash := utxoDirClash(cfg, id, node.UTXOIndexDir); clash != "" {
			logger.Printf("%s balance index disabled: %s overlaps %s", node.Name, node.UTXOIndexDir, clash)
			continue
		}
		m := a.newChainUtxo(node, chain, logger)
		a.chainUtxo[id] = m
		if store := a.familyWatch.stores[id]; store != nil {
			store.index, m.onApplied = m, store.requestScan
		}
	}
	if a.moneroWatch.Err() != nil {
		a.log.Print("Monero watch-only wallet state unavailable until the state file is repaired and the dashboard restarts")
	}
	a.ctx = context.Background()
	a.fees = []float64{}
	return a, nil
}

func removeLegacyUpgradeRequest(stateDir string) error {
	err := os.Remove(filepath.Join(stateDir, "upgrade.request"))
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return errors.New("legacy upgrade request cleanup unavailable")
}
func (a *app) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if a.cfg.bitcoinDisabled {
		a.render(w, pageData{Version: versionLabel(a.cfg.version)})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var chain blockchainInfo
	if err := a.rpc.call(ctx, "getblockchaininfo", []any{}, &chain); err != nil {
		a.render(w, a.loadingPageData())
		return
	}
	a.rememberSync(chain)
	var network networkInfo
	var peers []peerInfo
	var uptime float64
	networkErr := a.rpc.call(ctx, "getnetworkinfo", []any{}, &network)
	peersErr := a.rpc.call(ctx, "getpeerinfo", []any{}, &peers)
	uptimeErr := a.rpc.call(ctx, "uptime", []any{}, &uptime)
	usage := a.hostNodeUsage(nodeBitcoin)
	uptimeLabel := formatUptime(uptime)
	if uptimeErr != nil {
		uptimeLabel = "—"
		if seconds, ok := hostUptime(usage); ok {
			uptimeLabel = formatUptime(float64(seconds))
		}
	}
	if chain.SizeOnDisk <= 0 && usage.DiskBytes > 0 {
		chain.SizeOnDisk = int64(usage.DiskBytes)
	}
	if networkErr == nil && peersErr == nil {
		a.noteNodeNetwork(nodeBitcoin, network.Subversion, int64(len(peers)))
	}
	if network.Subversion == "" {
		network.Subversion = "Unknown"
	}

	inbound := 0
	for _, peer := range peers {
		if peer.Inbound {
			inbound++
		}
	}
	data := pageData{
		BitcoinEnabled:       true,
		InitialBlockDownload: chain.InitialBlockDownload,
		Version:              versionLabel(a.cfg.version), CoreVersion: network.Subversion, Uptime: uptimeLabel,
		Blocks: chain.Blocks, Headers: chain.Headers, Progress: chain.VerificationProgress * 100,
		PeersUnknown: peersErr != nil, Inbound: inbound, Outbound: len(peers) - inbound,
		Pruned: chain.Pruned, NodeGB: gib(chain.SizeOnDisk), NodeSizeBytes: chain.SizeOnDisk,
		NextBlock: chain.Blocks + 1, LastUpdate: time.Now().Format("15:04:05"), UpdatedAt: time.Now().Unix(),
		FeeNext: "—", Fee30m: "—", FeeHour: "—",
		BitcoinRPCEndpoint: a.cfg.bitcoinRPCEndpoint,
	}
	if !chain.InitialBlockDownload && chain.Blocks == chain.Headers {
		data.Progress = 100
	}
	data.StateLine = "Synchronizing the Bitcoin blockchain…"
	if !chain.InitialBlockDownload && chain.Blocks >= chain.Headers {
		data.StateLine = "Synced"
	}
	data.LastBlock = "—"
	if chain.BestBlockHash != "" {
		var header familyBlockHeader
		if a.rpc.call(ctx, "getblockheader", []any{chain.BestBlockHash}, &header) == nil && header.Time > 0 {
			if age := max(0, time.Now().Unix()-header.Time); age < 60 {
				data.LastBlock = "<1m ago"
			} else {
				data.LastBlock = formatUptime(float64(age)) + " ago"
			}
		}
	}
	if !chain.InitialBlockDownload {
		var mempool mempoolInfo
		if a.rpc.call(ctx, "getmempoolinfo", []any{}, &mempool) == nil {
			data.ShowMempool = true
			data.MempoolTxs = mempool.Size
			data.MempoolMB = float64(mempool.Bytes) / (1024 * 1024)
		}
		data.FeeNext = a.fee(ctx, 1)
		data.Fee30m = a.fee(ctx, 3)
		data.FeeHour = a.fee(ctx, 6)
	}
	a.render(w, data)
}

func (a *app) rememberSync(chain blockchainInfo) {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	if !chain.InitialBlockDownload {
		a.lastSync = nil
		return
	}
	a.lastSync = &syncSnapshot{blocks: chain.Blocks, headers: chain.Headers, progress: chain.VerificationProgress * 100, time: time.Now()}
}

func (a *app) loadingPageData() pageData {
	data := pageData{BitcoinEnabled: true, Loading: true, Version: versionLabel(a.cfg.version)}
	a.syncMu.RLock()
	defer a.syncMu.RUnlock()
	if a.lastSync != nil {
		data.LastKnownSync = true
		data.Blocks = a.lastSync.blocks
		data.Headers = a.lastSync.headers
		data.Progress = a.lastSync.progress
		data.LastSyncTime = a.lastSync.time.Format("2006-01-02 15:04:05 MST")
	}
	return data
}

func (a *app) render(w http.ResponseWriter, data pageData) {
	data.Chains = dashboardChains
	data.NodeName = a.monitor.nodeName
	data.Command = a.commandName()
	data.PasswordEnabled = a.hasDashboardPassword()
	data.UpdateNote = a.monitor.UpdateNote()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.template.ExecuteTemplate(w, "index.html", data); err != nil {
		a.log.Printf("render dashboard: %v", err)
	}
}

func (a *app) fee(ctx context.Context, blocks int) string {
	var estimate feeEstimate
	if err := a.rpc.call(ctx, "estimatesmartfee", []any{blocks}, &estimate); err != nil || estimate.FeeRate == nil || *estimate.FeeRate == 0 {
		return "—"
	}
	value := math.RoundToEven(*estimate.FeeRate*1000000) / 10
	return strconv.FormatFloat(value, 'f', -1, 64)
}
func diskUsage(path string) diskInfo {
	var stat syscall.Statfs_t
	if syscall.Statfs(path, &stat) != nil {
		return diskInfo{}
	}
	blockSize := statfsBlockSize(&stat)
	// used + avail, like df's percent: filesystem-reserved blocks are not data.
	used := (uint64(stat.Blocks) - uint64(stat.Bfree)) * blockSize
	return diskInfo{Total: used + uint64(stat.Bavail)*blockSize, Free: uint64(stat.Bavail) * blockSize}
}

func gib(v int64) float64 { return float64(v) / (1024 * 1024 * 1024) }
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func formatUptime(seconds float64) string {
	total := int64(seconds)
	days, rem := total/86400, total%86400
	hours, minutes := rem/3600, rem%3600/60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func versionLabel(version string) string {
	if version == "" || version == "dev" {
		return "dev"
	}
	if releaseVersion.MatchString(version) {
		return "v" + version
	}
	return version
}

func readVersionFile(path string) string {
	baked, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(baked))
}
