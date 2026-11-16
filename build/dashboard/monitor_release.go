package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func (m *Monitor) checkUpdates(ctx context.Context) {
	m.checkUpdatesWithNotification(ctx, true)
}

func (m *Monitor) checkUpdatesWithNotification(ctx context.Context, notify bool) {
	note := ""
	latestStack := m.latestReleaseTag(ctx, stackReleasesURL)
	if releaseVersion.MatchString(m.releaseBaseline) && latestStack != "" && newer(latestStack, m.releaseBaseline) {
		note = fmt.Sprintf("stack v%s available (running v%s)", latestStack, m.releaseBaseline)
		if notify && m.state.notifiedStack != latestStack {
			if !m.notificationSettings().AlertUpdates || m.sendTelegram(ctx, "🆕 "+note) {
				m.state.notifiedStack = latestStack
				m.saveState()
			}
		}
	}

	m.mu.Lock()
	m.updateNote = note
	m.mu.Unlock()
}

func (m *Monitor) loadState() {
	b, err := os.ReadFile(m.statePath)
	if err != nil {
		return
	}
	var state persistedMonitorState
	if json.Unmarshal(b, &state) == nil && (state.NotifiedStack == "" || releaseVersion.MatchString(state.NotifiedStack)) {
		m.state.notifiedStack = state.NotifiedStack
		m.hostEventsOffset = state.HostEventsOffset
		applyPersistedNodeState(&m.state, state.Bitcoin)
		applyPersistedNodeState(&m.moneroState, state.Monero)
		for id, saved := range state.Nodes {
			if normalizeNodeID(id) == "" || id == nodeBitcoin || id == nodeMonero {
				continue
			}
			if m.extraStates[id] == nil {
				m.extraStates[id] = &monitorState{}
			}
			applyPersistedNodeState(m.extraStates[id], saved)
		}
	}
}

func (m *Monitor) saveState() {
	b, err := json.Marshal(m.persistedState())
	if err != nil || writePrivateFileAtomic(m.statePath, append(b, '\n')) != nil {
		m.writeOutput("Monitor state write failed")
	}
}

func (m *Monitor) persistedState() persistedMonitorState {
	nodes := make(map[string]persistedNodeState, len(m.extraStates))
	for id, state := range m.extraStates {
		nodes[id] = persistedNodeFromState(*state)
	}
	return persistedMonitorState{
		NotifiedStack:    m.state.notifiedStack,
		Bitcoin:          persistedNodeFromState(m.state),
		Monero:           persistedNodeFromState(m.moneroState),
		Nodes:            nodes,
		HostEventsOffset: m.hostEventsOffset,
	}
}

func persistedNodeFromState(state monitorState) persistedNodeState {
	return persistedNodeState{
		Fails: state.fails, DownAlerted: state.downAlerted, ChainKnown: state.chainKnown,
		IBD: state.ibd, IBDTicks: state.ibdTicks, Synced: state.synced, SyncPending: state.syncPending, LastBlock: state.lastBlock,
		HasLastBlock: state.hasLastBlock, PendingBlock: state.pendingBlock, DiskAlerted: state.diskAlerted,
	}
}

func applyPersistedNodeState(state *monitorState, saved persistedNodeState) {
	state.fails, state.downAlerted, state.chainKnown = saved.Fails, saved.DownAlerted, false
	state.ibd, state.ibdTicks, state.syncPending = saved.IBD, saved.IBDTicks, saved.SyncPending
	state.synced = saved.Synced || (saved.ChainKnown && !saved.IBD && !saved.SyncPending)
	state.lastBlock, state.hasLastBlock, state.pendingBlock = saved.LastBlock, saved.HasLastBlock, saved.PendingBlock
	state.diskAlerted = saved.DiskAlerted
}

func (m *Monitor) latestReleaseTag(ctx context.Context, url string) string {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := m.client.Do(req)
	if err != nil {
		m.writeOutput("Release check failed")
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var release struct {
		Tag string `json:"tag_name"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release) != nil {
		return ""
	}
	return strings.TrimLeft(release.Tag, "v")
}

func newer(latest, current string) bool {
	a := numericParts(latest)
	b := numericParts(current)
	if len(a) == 0 {
		return false
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return len(a) > len(b)
}

func numericParts(version string) []uint64 {
	parts := versionNumber.FindAllString(version, -1)
	result := make([]uint64, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return nil
		}
		result = append(result, n)
	}
	return result
}
