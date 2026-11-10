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
	PruneTargetSize      int64   `json:"prune_target_size"`
	SizeOnDisk           int64   `json:"size_on_disk"`
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
	Inbound              int
	Outbound             int
	Pruned               bool
	PruneTargetGB        float64
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
	BitcoinRPCEndpoint   string
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
	dashboardAuth    *dashboardAuth
	sessions         *dashboardSessions
	loginLimiter     *loginLimiter
	passwordWork     *passwordWorkLimiter
	passwordAttempts chan struct{}
	controlMu        sync.Mutex
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
	a.watch = NewWatchStore(cfg.stateDir)
	if a.watch.ScanErr() != nil {
		a.log.Print("watch balance cache unavailable; pruned balance scans disabled until the cache is repaired and the dashboard restarts")
	}
	a.scanner = newPrunedScanner(a)
	a.monitor = NewMonitor(cfg.version, cfg.dataDir, cfg.stateDir)
	a.monitor.configureNodes(!cfg.bitcoinDisabled, cfg.moneroEnabled, cfg.moneroDataDir)
	a.monitor.configureAdditionalNodes(cfg.nodes)
	a.monitor.transitioning = a.nodeTransitioning
	if a.monitor.settingsErr != nil {
		a.log.Print("notification settings unavailable; alerts disabled until the state file is repaired and the dashboard restarts")
	}
	a.prices = NewPriceCache()
	a.moneroWatch = NewMoneroWatchStore(cfg)
	a.moneroFees = NewMoneroFeeHistory(cfg.stateDir)
	a.familyWatch = newFamilyWatchManager(a)
	a.tariWatch = NewTariWatchStore(cfg)
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
	_ = a.rpc.call(ctx, "getnetworkinfo", []any{}, &network)
	_ = a.rpc.call(ctx, "getpeerinfo", []any{}, &peers)
	uptimeErr := a.rpc.call(ctx, "uptime", []any{}, &uptime)
	uptimeLabel := formatUptime(uptime)
	if uptimeErr != nil {
		uptimeLabel = "N/A"
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
		Inbound: inbound, Outbound: len(peers) - inbound,
		Pruned: chain.Pruned, PruneTargetGB: gib(chain.PruneTargetSize), NodeGB: gib(chain.SizeOnDisk), NodeSizeBytes: chain.SizeOnDisk,
		NextBlock: chain.Blocks + 1, LastUpdate: time.Now().Format("15:04:05"),
		FeeNext: "—", Fee30m: "—", FeeHour: "—",
		BitcoinRPCEndpoint: a.cfg.bitcoinRPCEndpoint,
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
	data.NodeName = a.monitor.nodeName
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
	return diskInfo{Total: uint64(stat.Blocks) * blockSize, Free: uint64(stat.Bavail) * blockSize}
}

func gib(v int64) float64   { return float64(v) / (1024 * 1024 * 1024) }
func gibU(v uint64) float64 { return float64(v) / (1024 * 1024 * 1024) }
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
	return fmt.Sprintf("%dh %dm", hours, minutes)
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
