package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const (
	torProxy             = "172.29.0.25:9050"
	stackReleasesURL     = "https://api.github.com/repos/VijitSingh97/node-manager/releases/latest"
	pingEveryTicks       = 5
	downAfterTicks       = 3
	updateCheckEveryTick = 1440
	eventLogCap          = 1_000_000
)

var (
	releaseVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	versionNumber  = regexp.MustCompile(`\d+`)
)

type monitorState struct {
	fails         int
	downAlerted   bool
	chainKnown    bool
	ibd           bool
	syncPending   bool
	lastBlock     int64
	hasLastBlock  bool
	pendingBlock  int64
	diskAlerted   bool
	ticks         int
	pingDue       int
	notifiedStack string
}

type persistedMonitorState struct {
	NotifiedStack    string                        `json:"notified_stack"`
	Bitcoin          persistedNodeState            `json:"bitcoin"`
	Monero           persistedNodeState            `json:"monero"`
	Nodes            map[string]persistedNodeState `json:"nodes,omitempty"`
	HostEventsOffset int64                         `json:"host_events_offset,omitempty"`
}

type persistedNodeState struct {
	Fails        int   `json:"fails"`
	DownAlerted  bool  `json:"down_alerted"`
	ChainKnown   bool  `json:"chain_known"`
	IBD          bool  `json:"initial_block_download"`
	SyncPending  bool  `json:"sync_pending"`
	LastBlock    int64 `json:"last_block"`
	HasLastBlock bool  `json:"has_last_block"`
	PendingBlock int64 `json:"pending_block"`
	DiskAlerted  bool  `json:"disk_alerted"`
}

type Monitor struct {
	releaseBaseline string
	dataDir         string
	eventsLog       string
	statePath       string
	nodeName        string

	settingsMu         sync.RWMutex
	settings           notificationSettings
	settingsBaseline   notificationSettings
	settingsPath       string
	settingsErr        error
	settingsOverridden bool

	client         *http.Client
	freshTorClient func() *http.Client
	now            func() time.Time
	diskFreeGB     func(string) (float64, error)
	output         io.Writer
	tickEvery      time.Duration

	state          monitorState
	moneroState    monitorState
	bitcoinEnabled bool
	moneroEnabled  bool
	moneroDataDir  string
	extraStates    map[string]*monitorState
	extraDataDirs  map[string]string
	extraEnabled   map[string]bool
	transitioning  func(string) bool
	healthTicks    int
	healthPingDue  int

	mu               sync.RWMutex
	updateNote       string
	logMu            sync.Mutex
	hostEventsOffset int64
}

func NewMonitor(version, dataDir, stateDir string) *Monitor {
	eventsLog := filepath.Join(stateDir, "events.log")
	if value, ok := os.LookupEnv("EVENTS_LOG"); ok {
		eventsLog = value
	}
	settingsPath := filepath.Join(stateDir, "notification_settings.json")
	statePath := filepath.Join(stateDir, "monitor_state.json")
	baseline := notificationSettingsFromEnv()
	settings, overridden, settingsErr := loadNotificationSettings(settingsPath, baseline)
	if settingsErr != nil {
		settings = notificationSettings{}
	}
	releaseBaseline := version
	if !releaseVersion.MatchString(releaseBaseline) {
		releaseBaseline = readVersionFile("VERSION")
	}
	monitor := &Monitor{
		releaseBaseline:    releaseBaseline,
		dataDir:            dataDir,
		eventsLog:          eventsLog,
		statePath:          statePath,
		nodeName:           envOr("NODE_NAME", "bitcoin-node"),
		settings:           settings,
		settingsBaseline:   baseline,
		settingsPath:       settingsPath,
		settingsErr:        settingsErr,
		settingsOverridden: overridden,
		client:             newTorHTTPClient(torProxy),
		freshTorClient:     func() *http.Client { return newIsolatedTorHTTPClient(torProxy) },
		now:                time.Now,
		diskFreeGB:         freeGB,
		output:             os.Stdout,
		tickEvery:          time.Minute,
		extraStates:        make(map[string]*monitorState),
		extraDataDirs:      make(map[string]string),
		extraEnabled:       make(map[string]bool),
	}
	monitor.loadState()
	if monitor.hostEventsOffset == 0 {
		if stat, err := os.Stat(eventsLog); err == nil {
			monitor.hostEventsOffset = stat.Size()
		}
	}
	return monitor
}

func (m *Monitor) configureAdditionalNodes(nodes map[string]nodeConfig) {
	for id, node := range nodes {
		if !node.Supported {
			continue
		}
		m.extraEnabled[id] = node.Enabled
		m.extraDataDirs[id] = node.DataDir
		if m.extraStates[id] == nil {
			m.extraStates[id] = &monitorState{}
		}
	}
}

func (m *Monitor) configureNodes(bitcoinEnabled, moneroEnabled bool, moneroDataDir string) {
	m.bitcoinEnabled = bitcoinEnabled
	m.moneroEnabled = moneroEnabled
	m.moneroDataDir = moneroDataDir
}

func (m *Monitor) Run(ctx context.Context, blockchain func() map[string]any, network func() map[string]any) {
	onlinePending := true
	ticker := time.NewTicker(m.tickEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var info map[string]any
		if blockchain != nil {
			info = blockchain()
		}
		if onlinePending && info != nil {
			if ibd, ok := info["initialblockdownload"].(bool); ok {
				onlinePending = false
				if !ibd && m.notificationSettings().AlertMonitorOnline {
					m.sendTelegram(ctx, "🚀 node monitor online")
				}
			}
		}
		var netInfo map[string]any
		if info != nil && network != nil && m.state.ticks%updateCheckEveryTick == 0 {
			netInfo = network()
			if netInfo == nil {
				netInfo = map[string]any{}
			}
		}
		m.tick(ctx, info, netInfo)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Monitor) RunNodes(ctx context.Context, bitcoin, monero func() map[string]any) {
	bitcoinOnlinePending, moneroOnlinePending := true, true
	ticker := time.NewTicker(m.tickEvery)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		var bitcoinInfo, moneroInfo map[string]any
		type nodeResult struct {
			bitcoin bool
			info    map[string]any
		}
		results := make(chan nodeResult, 2)
		pending := 0
		if m.bitcoinEnabled && bitcoin != nil {
			pending++
			go func() { results <- nodeResult{bitcoin: true, info: bitcoin()} }()
		}
		if m.moneroEnabled && monero != nil {
			pending++
			go func() { results <- nodeResult{info: monero()} }()
		}
		for range pending {
			var result nodeResult
			select {
			case result = <-results:
			case <-ctx.Done():
				return
			}
			if result.bitcoin {
				bitcoinInfo = result.info
			} else {
				moneroInfo = result.info
			}
		}
		if m.bitcoinEnabled {
			bitcoinOnlinePending = m.announceOnline(ctx, "Bitcoin", &m.state, bitcoinInfo, bitcoinOnlinePending)
		}
		if m.moneroEnabled {
			moneroOnlinePending = m.announceOnline(ctx, "Monero", &m.moneroState, moneroInfo, moneroOnlinePending)
		}
		m.tickNodes(ctx, bitcoinInfo, moneroInfo)
		m.forwardHostEvents(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Monitor) announceOnline(ctx context.Context, chain string, state *monitorState, info map[string]any, pending bool) bool {
	if !pending || info == nil {
		return pending
	}
	if ibd, ok := info["initialblockdownload"].(bool); ok {
		if !ibd && !state.ibd && !state.syncPending && m.notificationSettings().AlertMonitorOnline {
			m.sendTelegram(ctx, chain+": 🚀 monitor online")
		}
		return false
	}
	return pending
}

func (m *Monitor) UpdateNote() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.updateNote
}

func (m *Monitor) tick(ctx context.Context, info, network map[string]any) {
	ticks := m.state.ticks
	healthy, hasIBD, ibd := m.tickNode(ctx, &m.state, "", m.dataDir, m.notificationSettings().AlertNewBlock, info)
	if ticks >= m.state.pingDue && m.pingHealthchecks(ctx) {
		m.state.pingDue = ticks + pingEveryTicks
	}
	if healthy && network != nil && ticks%updateCheckEveryTick == 0 {
		m.checkUpdatesWithNotification(ctx, hasIBD && !ibd)
	}
	m.state.ticks++
}

func (m *Monitor) tickNodes(ctx context.Context, bitcoinInfo, moneroInfo map[string]any) {
	before := m.persistedState()
	settings := m.notificationSettings()
	anySynced := false
	if m.bitcoinEnabled {
		healthy, hasIBD, ibd := m.tickNode(ctx, &m.state, "Bitcoin", m.dataDir, settings.AlertNewBlock, bitcoinInfo)
		anySynced = anySynced || (healthy && hasIBD && !ibd)
		m.state.ticks++
	}
	if m.moneroEnabled {
		healthy, hasIBD, ibd := m.tickNode(ctx, &m.moneroState, "Monero", m.moneroDataDir, settings.AlertNewBlockMonero, moneroInfo)
		anySynced = anySynced || (healthy && hasIBD && !ibd)
		m.moneroState.ticks++
	}
	if !m.bitcoinEnabled && !m.moneroEnabled {
		return
	}
	if m.healthTicks >= m.healthPingDue && m.pingHealthchecks(ctx) {
		m.healthPingDue = m.healthTicks + pingEveryTicks
	}
	if m.healthTicks%updateCheckEveryTick == 0 {
		m.checkUpdatesWithNotification(ctx, anySynced)
	}
	m.healthTicks++
	if !reflect.DeepEqual(before, m.persistedState()) {
		m.saveState()
	}
}

func (m *Monitor) RunConfiguredNodes(ctx context.Context, probes map[string]func() map[string]any) {
	onlinePending := make(map[string]bool, len(probes))
	for id := range probes {
		onlinePending[id] = true
	}
	ticker := time.NewTicker(m.tickEvery)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		results := make(chan struct {
			id   string
			info map[string]any
		}, len(probes))
		started := 0
		for id, probe := range probes {
			if m.transitioning != nil && m.transitioning(id) {
				continue
			}
			started++
			go func(id string, probe func() map[string]any) {
				results <- struct {
					id   string
					info map[string]any
				}{id, probe()}
			}(id, probe)
		}
		infos := make(map[string]map[string]any, len(probes))
		for range started {
			select {
			case result := <-results:
				infos[result.id] = result.info
			case <-ctx.Done():
				return
			}
		}
		for id, info := range infos {
			if m.transitioning != nil && m.transitioning(id) {
				continue
			}
			if info != nil && info["active"] == false {
				continue
			}
			state := m.monitorStateFor(id)
			if state != nil {
				onlinePending[id] = m.announceOnline(ctx, nodeDisplayName(id), state, info, onlinePending[id])
			}
		}
		m.tickConfiguredNodes(ctx, infos)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Monitor) monitorStateFor(id string) *monitorState {
	switch id {
	case nodeBitcoin:
		return &m.state
	case nodeMonero:
		return &m.moneroState
	default:
		return m.extraStates[id]
	}
}

func nodeDisplayName(id string) string {
	switch id {
	case nodeBitcoin:
		return "Bitcoin"
	case nodeMonero:
		return "Monero"
	case nodeTari:
		return "Tari"
	case nodeLitecoin:
		return "Litecoin"
	case nodeDogecoin:
		return "Dogecoin"
	case nodeBitcoinCash:
		return "Bitcoin Cash"
	case nodeZcash:
		return "Zcash"
	}
	return id
}

func (m *Monitor) nodeDataDir(id string) string {
	if id == nodeBitcoin {
		return m.dataDir
	}
	if id == nodeMonero {
		return m.moneroDataDir
	}
	return m.extraDataDirs[id]
}

func alertNewBlockFor(settings notificationSettings, id string) bool {
	switch id {
	case nodeBitcoin:
		return settings.AlertNewBlock
	case nodeMonero:
		return settings.AlertNewBlockMonero
	case nodeTari:
		return settings.AlertNewBlockTari
	case nodeLitecoin:
		return settings.AlertNewBlockLitecoin
	case nodeDogecoin:
		return settings.AlertNewBlockDogecoin
	case nodeBitcoinCash:
		return settings.AlertNewBlockBitcoinCash
	case nodeZcash:
		return settings.AlertNewBlockZcash
	}
	return false
}

func (m *Monitor) tickConfiguredNodes(ctx context.Context, infos map[string]map[string]any) {
	before := m.persistedState()
	settings := m.notificationSettings()
	anySynced, activeCount := false, 0
	for id, info := range infos {
		if m.transitioning != nil && m.transitioning(id) {
			continue
		}
		if info != nil && info["active"] == false {
			continue
		}
		state := m.monitorStateFor(id)
		if state == nil {
			continue
		}
		activeCount++
		healthy, hasIBD, ibd := m.tickNode(ctx, state, nodeDisplayName(id), m.nodeDataDir(id), alertNewBlockFor(settings, id), info)
		anySynced = anySynced || (healthy && hasIBD && !ibd)
		state.ticks++
	}
	if activeCount == 0 {
		return
	}
	if m.healthTicks >= m.healthPingDue && m.pingHealthchecks(ctx) {
		m.healthPingDue = m.healthTicks + pingEveryTicks
	}
	if m.healthTicks%updateCheckEveryTick == 0 {
		m.checkUpdatesWithNotification(ctx, anySynced)
	}
	m.healthTicks++
	if !reflect.DeepEqual(before, m.persistedState()) {
		m.saveState()
	}
}

func (m *Monitor) tickNode(ctx context.Context, state *monitorState, chain, dataDir string, alertNewBlock bool, info map[string]any) (bool, bool, bool) {
	settings := m.notificationSettings()
	downAfterMinutes := settings.NodeDownAfterMinutes
	if downAfterMinutes < 1 {
		downAfterMinutes = defaultNodeDownAfterMinutes
	}
	lowDiskGiB := settings.LowDiskGiB
	if lowDiskGiB < 1 {
		lowDiskGiB = defaultLowDiskGiB
	}
	healthy := info != nil
	ibd, hasIBD := false, false
	if healthy {
		ibd, hasIBD = info["initialblockdownload"].(bool)
		if state.ibd && hasIBD && !ibd {
			state.syncPending = true
		}
		if hasIBD {
			state.chainKnown = true
			state.ibd = ibd
		}
		if state.downAlerted && hasIBD && !ibd {
			if m.sendOperationalAlert(ctx, settings.AlertNodeRecovered, chainAlert(chain, "🟢 node recovered — RPC is answering again")) {
				state.downAlerted = false
			}
		}
		state.fails = 0
		if state.syncPending && hasIBD && !ibd && m.sendOperationalAlert(ctx, settings.AlertSyncComplete, chainAlert(chain, "✅ initial sync complete at height "+valueString(info["blocks"]))) {
			state.syncPending = false
		}

		if blocks, ok := integer(info["blocks"]); ok {
			if alertNewBlock && state.hasLastBlock && hasIBD && !ibd && blocks > state.lastBlock && blocks-state.lastBlock <= 6 {
				state.pendingBlock = blocks
			}
			state.lastBlock = blocks
			state.hasLastBlock = true
		}
		if !alertNewBlock || (hasIBD && ibd) {
			state.pendingBlock = 0
		} else if state.pendingBlock != 0 && hasIBD && !ibd && m.sendOperationalTelegram(ctx, chainAlert(chain, "🟧 new block "+strconv.FormatInt(state.pendingBlock, 10))) {
			state.pendingBlock = 0
		}

		free, err := m.diskFreeGB(dataDir)
		if err != nil {
			m.writeOutput("Monitor tick failed: disk usage unavailable")
			return healthy, hasIBD, ibd
		}
		if free < float64(lowDiskGiB) && hasIBD && !ibd {
			if !state.diskAlerted {
				state.diskAlerted = m.sendOperationalAlert(ctx, settings.AlertLowDisk, chainAlert(chain, fmt.Sprintf("💾 disk low: %.0f GiB free", free)))
			}
		} else {
			state.diskAlerted = false
		}
	} else {
		state.fails++
		if state.fails >= downAfterMinutes && !state.downAlerted && state.chainKnown && !state.ibd {
			message := fmt.Sprintf("🔴 node down — RPC unreachable for %d minutes", downAfterMinutes)
			state.downAlerted = m.sendOperationalAlert(ctx, settings.AlertNodeDown, chainAlert(chain, message))
		}
	}
	return healthy, hasIBD, ibd
}

func chainAlert(chain, message string) string {
	if chain == "" {
		return message
	}
	return chain + ": " + message
}
func freeGB(path string) (float64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return float64(stat.Bavail) * float64(statfsBlockSize(&stat)) / float64(uint64(1)<<30), nil
}

func integer(value any) (int64, bool) {
	switch n := value.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		v := int64(n)
		return v, float64(v) == n
	case json.Number:
		v, err := n.Int64()
		return v, err == nil
	default:
		return 0, false
	}
}

func valueString(value any) string {
	if n, ok := integer(value); ok {
		return strconv.FormatInt(n, 10)
	}
	return fmt.Sprint(value)
}

func envOr(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}

func enabled(value string) bool {
	return value != "" && value != "0" && value != "false" && value != "False"
}
