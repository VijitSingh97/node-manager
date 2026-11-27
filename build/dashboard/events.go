package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	eventsPageDefault = 50
	eventsPageMax     = 200
	eventsReadCap     = 2 * eventLogCap
	eventsSeenName    = "events_seen.json"
	hiddenEventText   = "Details hidden because they may include a secret."
)

type dashboardEvent struct {
	ID       int64  `json:"id"`
	Time     string `json:"time"`
	Category string `json:"category"`
	Severity string `json:"severity"`
	Node     string `json:"node,omitempty"`
	Text     string `json:"text"`
}

type controlResult struct {
	name, id, category, severity, text string
}

var (
	eventCategories    = []string{"node", "chain", "host", "settings", "security", "index", "wallet", "system", "alerts"}
	dashboardEventLine = regexp.MustCompile(`^(Node|Chain|Settings|Security|Index|Wallet|System|Alerts)( warning| error)?: `)
	// redact_last_line's rule from scripts/helper-common.sh plus Telegram bot tokens; "pub" needs an xpub prefix so "Published" stays readable.
	eventSecretWords = regexp.MustCompile(`passw|secret|token|auth|login|cookie|phrase|seed|mnemonic|private|prv|[a-z]pub[0-9a-z]|api.?key|bearer|onion|://[^/ ]*@|[0-9]{5,}:[a-z0-9_-]{30,}`)
	eventNodeIDs     = map[string]string{}
	eventNodeName    = func() *regexp.Regexp {
		var names []string
		for _, id := range nodeOrder {
			eventNodeIDs[id], eventNodeIDs[strings.ToLower(nodeDisplayName(id))] = id, id
		}
		for name := range eventNodeIDs {
			names = append(names, regexp.QuoteMeta(name))
		}
		// Longest first, so "bitcoin cash" wins over "bitcoin" at the same position.
		sort.Slice(names, func(i, j int) bool {
			return len(names[i]) > len(names[j]) || len(names[i]) == len(names[j]) && names[i] < names[j]
		})
		return regexp.MustCompile(`(?i)\b(` + strings.Join(names, "|") + `)\b`)
	}()
	monitorEventKinds = []struct{ phrase, category, severity string }{
		{"🔴 node down", "node", "error"},
		{"🟢 node recovered", "node", "info"},
		{"🚀 monitor online", "node", "info"},
		{"✅ initial sync complete", "chain", "info"},
		{"🟧 new block", "chain", "info"},
		{"💾 disk low", "host", "warning"},
	}
)

func eventTextHidden(text string) bool {
	if eventSecretWords.MatchString(strings.ToLower(text)) {
		return true
	}
	return slices.ContainsFunc(strings.FieldsFunc(text, func(r rune) bool {
		return !strings.ContainsRune("+=_-", r) && (r > unicode.MaxASCII || !unicode.IsLetter(r) && !unicode.IsDigit(r))
	}), func(word string) bool { return len(word) >= 40 })
}

func eventNode(text string) string {
	return eventNodeIDs[strings.ToLower(eventNodeName.FindString(text))]
}

func hostEventKind(text string) (string, string) {
	switch {
	case strings.Contains(text, "thermal throttle released"), strings.HasPrefix(text, "Tor working again"), strings.HasPrefix(text, "Published LAN listeners again"):
		return "host", "info"
	case strings.Contains(text, "is not coming up"), strings.Contains(text, "failed to start"), strings.Contains(text, " install failed"), strings.Contains(text, " activation failed"):
		return "node", "error"
	case strings.HasSuffix(text, " deleted"):
		return "node", "info"
	case strings.Contains(text, " is not on this host"):
		return "host", "error"
	}
	return "host", "warning"
}

func monitorEventKind(text string) (string, string, bool) {
	if strings.HasPrefix(text, "🆕 stack v") {
		return "system", "info", true
	}
	if strings.HasPrefix(text, "healthchecks ping failed: ") {
		return "alerts", "warning", true
	}
	if chain, message, ok := strings.Cut(text, ": "); ok && chain != "" && nodeDisplayName(eventNode(chain)) == chain {
		for _, kind := range monitorEventKinds {
			if strings.HasPrefix(message, kind.phrase) {
				return kind.category, kind.severity, true
			}
		}
	}
	return "system", "info", false
}

func parseEvent(text string) (dashboardEvent, bool) {
	event := dashboardEvent{Severity: "info", Text: text}
	trusted := false
	switch {
	// Telegram copies of Host lines are already listed under their Host line.
	case strings.HasPrefix(text, "🌡 "), strings.HasPrefix(text, "🔴 "):
		return event, false
	case strings.HasPrefix(text, "Host: "):
		event.Text = strings.TrimPrefix(text, "Host: ")
		event.Category, event.Severity = hostEventKind(event.Text)
	default:
		if match := dashboardEventLine.FindStringSubmatch(text); match != nil {
			event.Category, event.Text, trusted = strings.ToLower(match[1]), text[len(match[0]):], true
			if match[2] != "" {
				event.Severity = match[2][1:]
			}
		} else {
			event.Category, event.Severity, trusted = monitorEventKind(text)
		}
	}
	event.Node = eventNode(event.Text)
	if !trusted && eventTextHidden(event.Text) {
		event.Text = hiddenEventText
	}
	return event, true
}

func readEventTail(path string, limit int64) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, errors.New("events log is not a regular file")
	}
	base := max(0, info.Size()-limit)
	data, err := io.ReadAll(io.NewSectionReader(f, base, info.Size()-base))
	return data, base, err
}

// shortcut: ids are byte offsets, so a page cursor taken before a rotation can repeat or skip lines; key events by sequence if that matters.
func (m *Monitor) readEvents(category, node string, before int64, limit int) ([]dashboardEvent, int64, error) {
	m.logMu.Lock()
	data, base, err := readEventTail(m.eventsLog, eventsReadCap)
	m.logMu.Unlock()
	if errors.Is(err, fs.ErrNotExist) {
		return []dashboardEvent{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	data = data[:bytes.LastIndexByte(data, '\n')+1]
	if base > 0 {
		skip := bytes.IndexByte(data, '\n') + 1
		data, base = data[skip:], base+int64(skip)
	}
	if before > 0 {
		data = data[:min(int64(len(data)), max(0, before-base))]
		data = data[:bytes.LastIndexByte(data, '\n')+1]
	}
	events := []dashboardEvent{}
	for len(data) > 0 && len(events) < limit {
		start := bytes.LastIndexByte(data[:len(data)-1], '\n') + 1
		line, offset := string(data[start:len(data)-1]), base+int64(start)
		data = data[:start]
		stamp, text, ok := strings.Cut(line, " ")
		when, err := time.Parse(time.RFC3339, stamp)
		if !ok || err != nil {
			continue
		}
		event, keep := parseEvent(strings.TrimSpace(text))
		if !keep || category != "" && event.Category != category || node != "" && event.Node != node {
			continue
		}
		event.ID, event.Time = offset, when.UTC().Format(time.RFC3339)
		events = append(events, event)
	}
	next := int64(0)
	if len(events) == limit && len(data) > 0 {
		next = events[len(events)-1].ID
	}
	return events, next, nil
}

func (a *app) apiEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if rejectCSRF(w, r) || a.rejectUnsafeSettingsHost(w, r) {
		return
	}
	query := r.URL.Query()
	category, node := query.Get("category"), query.Get("node")
	before, limit := int64(0), eventsPageDefault
	var beforeErr, limitErr error
	if raw := query.Get("before"); raw != "" {
		before, beforeErr = strconv.ParseInt(raw, 10, 64)
	}
	if raw := query.Get("limit"); raw != "" {
		limit, limitErr = strconv.Atoi(raw)
	}
	if beforeErr != nil || limitErr != nil || before < 0 || limit < 1 || limit > eventsPageMax ||
		category != "" && !slices.Contains(eventCategories, category) || node != "" && normalizeNodeID(node) == "" {
		http.Error(w, "invalid events query", http.StatusBadRequest)
		return
	}
	view := map[string]any{"events": []dashboardEvent{}}
	if a.monitor != nil {
		events, next, err := a.monitor.readEvents(category, node, before, limit)
		if err != nil {
			http.Error(w, "events log unavailable", http.StatusServiceUnavailable)
			return
		}
		view["events"] = events
		if next > 0 {
			view["next"] = next
		}
	}
	writeJSON(w, view)
}

func (a *app) recordEvent(category, severity, text string) {
	if a.monitor == nil || a.cfg.stateDir == "" {
		return
	}
	if severity != "" {
		category += " " + severity
	}
	a.monitor.logEvent(category + ": " + text)
}

func (a *app) recordSignIn(r *http.Request) {
	remote := r.RemoteAddr
	if client, ok := a.loginClient(r); ok {
		remote = client
	}
	if host, _, err := net.SplitHostPort(remote); err == nil {
		a.recordEvent("Security", "", "Signed in from "+host+".")
		return
	}
	a.recordEvent("Security", "", "Signed in.")
}

func changedSettings(before, after notificationSettings) []string {
	var old, current map[string]json.RawMessage
	b, _ := json.Marshal(before)
	c, _ := json.Marshal(after)
	if json.Unmarshal(b, &old) != nil || json.Unmarshal(c, &current) != nil {
		return nil
	}
	var changed []string
	for key, value := range current {
		if !bytes.Equal(old[key], value) {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed
}

func stateSeverity(state string) string {
	switch state {
	case "rejected":
		return "warning"
	case "failed":
		return "error"
	}
	return ""
}

func firstSentence(text string) string {
	if end := strings.Index(text, ". "); end >= 0 {
		return text[:end+1]
	}
	return text
}

func (a *app) hostResult(status hostControlStatus) controlResult {
	category := "Settings"
	switch status.Action {
	case "install", "delete", "migrate":
		category = "Node"
	case "utxo_index", "utxo_seed":
		category = "Index"
	}
	text := canonicalHostStatusMessage(status, a.commandName())
	if status.Node != "" && !strings.Contains(text, nodeDisplayName(status.Node)) {
		text = nodeDisplayName(status.Node) + ": " + text
	}
	if detail := hostStatusDetail(status); detail != "" && !eventTextHidden(detail) {
		text += " " + detail
	}
	var restarted []string
	for _, id := range nodeOrder {
		if slices.Contains(status.Restarted, id) {
			restarted = append(restarted, nodeDisplayName(id))
		}
	}
	if status.State == "applied" && len(restarted) > 0 {
		text += " Restarted " + strings.Join(restarted, ", ") + "."
	}
	return controlResult{"node-control", status.ID, category, stateSeverity(status.State), text}
}

func (a *app) controlResults() []controlResult {
	var results []controlResult
	if status, ok := a.rawHostStatus(); ok {
		results = append(results, a.hostResult(status))
	} else if status := a.readNodeControlStatus(); status != nil {
		text := status.Message
		if eventTextHidden(text) {
			text = firstSentence(text)
		}
		results = append(results, controlResult{"node-control", status.ID, "Node", stateSeverity(status.State), text})
	}
	if status := a.readPortControlStatus(); status != nil {
		results = append(results, controlResult{"port-control", status.ID, "Settings", stateSeverity(status.State), status.Message})
	}
	if status, ok := a.savedCredentialStatus(); ok {
		text := map[string]string{"applied": " RPC credentials reset.", "rejected": " RPC credential reset was rejected; nothing changed.", "failed": " RPC credential reset failed."}[status.State]
		if text != "" {
			results = append(results, controlResult{"credential-control", status.ID, "Security", stateSeverity(status.State), nodeDisplayName(status.Node) + text})
		}
	}
	return results
}

func (a *app) logControlResults() {
	if a.cfg.stateDir == "" {
		return
	}
	path := filepath.Join(a.cfg.stateDir, eventsSeenName)
	if a.controlSeen == nil {
		a.controlSeen = map[string]string{}
		if saved, err := readSmallFile(path, 4096); err != nil || json.Unmarshal(saved, &a.controlSeen) != nil {
			// First run: results already on disk happened before the event log recorded them.
			for _, result := range a.controlResults() {
				a.controlSeen[result.name] = result.id
			}
			b, _ := json.Marshal(a.controlSeen)
			_ = writePrivateFileAtomic(path, append(b, '\n'))
			return
		}
	}
	changed := false
	for _, result := range a.controlResults() {
		if result.id == "" || a.controlSeen[result.name] == result.id {
			continue
		}
		a.controlSeen[result.name] = result.id
		changed = true
		a.recordEvent(result.category, result.severity, result.text)
	}
	if changed {
		b, _ := json.Marshal(a.controlSeen)
		if writePrivateFileAtomic(path, append(b, '\n')) != nil {
			a.log.Print("events: control result cursor could not be saved")
		}
	}
}

func (a *app) indexEvent(id string) func(severity, text string) {
	return func(severity, text string) {
		a.recordEvent("Index", severity, nodeDisplayName(id)+" balance index "+text)
	}
}

func seedEvent(err error) (string, string) {
	switch {
	case err == nil:
		return "", "seed finished."
	case errors.Is(err, context.Canceled):
		return "warning", "seed cancelled."
	}
	return "error", "seed failed: " + seedErrorClass(err) + "."
}

func verifyEvent(res verifyResult) (string, string) {
	switch {
	case len(res.Refusals) > 0:
		return "warning", "verify did not run: " + res.Refusals[0] + "."
	case res.Match != nil && *res.Match:
		return "", fmt.Sprintf("verified at height %d: it matches the node.", res.Height)
	case res.Match != nil:
		return "error", fmt.Sprintf("verify at height %d found a mismatch with the node.", res.Height)
	}
	return "error", "verify could not finish."
}
