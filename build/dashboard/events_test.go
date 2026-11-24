package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type eventsPage struct {
	Events []dashboardEvent `json:"events"`
	Next   int64            `json:"next"`
}

func eventsRequest(t *testing.T, handler http.Handler, query string) (*httptest.ResponseRecorder, eventsPage) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/events"+query, nil)
	req.SetBasicAuth("admin", "dashboard-secret")
	req.Header.Set("X-Requested-With", "fetch")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	var page eventsPage
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
	}
	return w, page
}

func writeEventsLog(t *testing.T, a *app, lines ...string) {
	t.Helper()
	if err := os.WriteFile(a.monitor.eventsLog, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}

func eventTexts(events []dashboardEvent) []string {
	texts := make([]string, len(events))
	for i, event := range events {
		texts[i] = event.Text
	}
	return texts
}

func TestEventsAPIClassifiesFiltersAndPages(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	writeEventsLog(t, a,
		"2026-11-20T10:00:00Z Litecoin: 🔴 node down — RPC unreachable for 3 minutes",
		"not a timestamp at all",
		"2026-11-20T10:01:00Z Host: thermal throttle at 90C — capped bitcoincash at 1.5 CPUs",
		"2026-11-20T10:01:05Z 🌡 thermal throttle at 90C — capped bitcoincash at 1.5 CPUs",
		"2026-11-20T10:02:00Z Litecoin: 🟢 node recovered — RPC is answering again",
		"2026-11-20T10:03:00Z Node error: Bitcoin could not be changed; the prior state was restored.",
		"2026-11-20T10:04:00Z Security: Signed in from 192.168.1.20.",
		"2026-11-20T10:05:00Z Host: Published LAN listeners again now that their address is back: Bitcoin RPC.",
		"2026-11-20T10:06:00Z healthchecks ping failed: HTTP 404",
		"2026-11-20T10:07:00Z 🆕 stack v1.41.0 available (running v1.40.3)",
		"2026-11-20T10:08:00Z Index: Bitcoin balance index seed finished.",
		"2026-11-20T10:09:00Z Host: half-written line",
	)
	w, page := eventsRequest(t, handler, "")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d, cache %q: %s", w.Code, w.Header().Get("Cache-Control"), w.Body.String())
	}
	want := []dashboardEvent{
		{Category: "index", Severity: "info", Node: "bitcoin", Text: "Bitcoin balance index seed finished.", Time: "2026-11-20T10:08:00Z"},
		{Category: "system", Severity: "info", Text: "🆕 stack v1.41.0 available (running v1.40.3)", Time: "2026-11-20T10:07:00Z"},
		{Category: "alerts", Severity: "warning", Text: "healthchecks ping failed: HTTP 404", Time: "2026-11-20T10:06:00Z"},
		{Category: "host", Severity: "info", Node: "bitcoin", Text: "Published LAN listeners again now that their address is back: Bitcoin RPC.", Time: "2026-11-20T10:05:00Z"},
		{Category: "security", Severity: "info", Text: "Signed in from 192.168.1.20.", Time: "2026-11-20T10:04:00Z"},
		{Category: "node", Severity: "error", Node: "bitcoin", Text: "Bitcoin could not be changed; the prior state was restored.", Time: "2026-11-20T10:03:00Z"},
		{Category: "node", Severity: "info", Node: "litecoin", Text: "Litecoin: 🟢 node recovered — RPC is answering again", Time: "2026-11-20T10:02:00Z"},
		{Category: "host", Severity: "warning", Node: "bitcoincash", Text: "thermal throttle at 90C — capped bitcoincash at 1.5 CPUs", Time: "2026-11-20T10:01:00Z"},
		{Category: "node", Severity: "error", Node: "litecoin", Text: "Litecoin: 🔴 node down — RPC unreachable for 3 minutes", Time: "2026-11-20T10:00:00Z"},
	}
	if len(page.Events) != len(want) || page.Next != 0 {
		t.Fatalf("events = %q, next %d", eventTexts(page.Events), page.Next)
	}
	for i := range want {
		got := page.Events[i]
		want[i].ID = got.ID
		if got != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, got, want[i])
		}
	}

	_, page = eventsRequest(t, handler, "?category=node&node=litecoin")
	if texts := eventTexts(page.Events); len(texts) != 2 || !strings.Contains(texts[0], "recovered") || !strings.Contains(texts[1], "down") {
		t.Fatalf("filtered events = %q", texts)
	}

	_, first := eventsRequest(t, handler, "?limit=4")
	if len(first.Events) != 4 || first.Next != first.Events[3].ID {
		t.Fatalf("first page = %q, next %d", eventTexts(first.Events), first.Next)
	}
	_, second := eventsRequest(t, handler, "?limit=4&before="+jsonNumber(first.Next))
	if len(second.Events) != 4 || second.Events[0].Text != want[4].Text {
		t.Fatalf("second page = %q", eventTexts(second.Events))
	}
	_, last := eventsRequest(t, handler, "?limit=4&before="+jsonNumber(second.Next))
	if len(last.Events) != 1 || last.Next != 0 || last.Events[0].Text != want[8].Text {
		t.Fatalf("last page = %q, next %d", eventTexts(last.Events), last.Next)
	}

	for _, query := range []string{"?category=nope", "?node=dash", "?limit=0", "?limit=201", "?before=-1", "?before=x"} {
		if w, _ := eventsRequest(t, handler, query); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", query, w.Code)
		}
	}
}

func jsonNumber(value int64) string {
	b, _ := json.Marshal(value)
	return string(b)
}

func TestEventsAPIRequiresSignInAndFetchHeader(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	writeEventsLog(t, a, "2026-11-20T10:00:00Z Security: Signed out.\n")
	if got := anonymousRequest(t, handler, http.MethodGet, "/api/events"); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", got.Code)
	}
	if got := request(t, handler, http.MethodGet, "/api/events", ""); got.Code != http.StatusForbidden {
		t.Fatalf("without X-Requested-With = %d", got.Code)
	}
	if w, page := eventsRequest(t, handler, ""); w.Code != http.StatusOK || len(page.Events) != 1 {
		t.Fatalf("signed in = %d %s", w.Code, w.Body.String())
	}
}

func TestEventsAPIHidesSecretsFromHelperLines(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	secrets := []string{
		"123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw2",
		"http://rpcuser:hunter2@127.0.0.1:8332",
		"rpcpassword=hunter2",
		"xpub6CUGRUonZSQ4TWtTMmzXdrXDtyPWKiKbERr4d5qkSmh5h17C1TjvMt7DJ9Qve4dRxm91CDv6cNfKsq2mK1rMsJKhtRUPZz7MQtp3y6atC1U",
		"vww6ybal4bd7szmgncyruucpgfkqahzddi37ktceo3ah7ngmcopnpyyd.onion",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"telegram token abc",
	}
	lines := []string{"2026-11-20T10:00:00Z Host: Bitcoin is not coming up: it kept failing to start for 10 minutes, so it was stopped. Last error: " + secrets[0]}
	for _, secret := range secrets[1:] {
		lines = append(lines, "2026-11-20T10:00:01Z Host: Monero failed to start ("+secret+"); recreated it.")
		lines = append(lines, "2026-11-20T10:00:02Z Litecoin: unexpected "+secret)
	}
	writeEventsLog(t, a, append(lines, "")...)
	w, page := eventsRequest(t, handler, "")
	for _, secret := range secrets {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("response leaks %q", secret)
		}
	}
	if len(page.Events) != len(lines) {
		t.Fatalf("events = %d, want %d", len(page.Events), len(lines))
	}
	last := page.Events[len(page.Events)-1]
	if last.Text != hiddenEventText || last.Category != "node" || last.Severity != "error" || last.Node != "bitcoin" {
		t.Fatalf("hidden host event = %+v", last)
	}
}

func TestEventsAPIReadsOnlyTheTailOfAnOversizedLog(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	line := "2026-11-20T10:00:00Z Host: thermal throttle released (cooled) — restored bitcoin " + strings.Repeat("x", 30) + "\n"
	body := strings.Repeat(line, eventsReadCap/len(line)+10) + "2026-11-20T11:00:00Z Security: Signed out.\n"
	if err := os.WriteFile(a.monitor.eventsLog, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, page := eventsRequest(t, handler, "?limit=200")
	if len(page.Events) != 200 || page.Events[0].Text != "Signed out." || page.Next == 0 {
		t.Fatalf("got %d events, first %q, next %d", len(page.Events), page.Events[0].Text, page.Next)
	}
	for _, event := range page.Events[1:] {
		if event.Category != "host" || event.Severity != "info" || event.Node != "bitcoin" {
			t.Fatalf("tail event = %+v", event)
		}
	}
}

func TestEventsAPIWithoutALogIsEmpty(t *testing.T) {
	_, handler := testApp(t, &rpcFixture{}, "")
	if w, page := eventsRequest(t, handler, ""); w.Code != http.StatusOK || page.Events == nil || len(page.Events) != 0 || !strings.Contains(w.Body.String(), `"events":[]`) {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}

func TestDashboardActionsWriteEvents(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	req := httptest.NewRequest(http.MethodPost, "/api/settings?scope=global", strings.NewReader(`{"alert_low_disk":false,"low_disk_gib":20,"telegram_bot_token":"123456789:events-flow-token","telegram_enabled":true,"telegram_chat_id":"42"}`))
	req.SetBasicAuth("admin", "dashboard-secret")
	req.Header.Set("X-Requested-With", "fetch")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("settings save = %d %s", w.Code, w.Body.String())
	}
	a.recordSignIn(httptest.NewRequest(http.MethodPost, "/login", nil))
	a.indexEvent(nodeLitecoin)(seedEvent(errors.New("boom")))
	log, err := os.ReadFile(a.monitor.eventsLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "events-flow-token") || strings.Contains(string(log), " 42") {
		t.Fatalf("event log holds a settings value: %s", log)
	}
	_, page := eventsRequest(t, handler, "")
	got := eventTexts(page.Events)
	want := []string{
		"Litecoin balance index seed failed: seed failed.",
		"Signed in from 192.0.2.1.",
		"Settings changed: alert_low_disk, low_disk_gib, telegram_bot_token, telegram_chat_id, telegram_enabled.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events = %q", got)
	}
	if page.Events[0].Severity != "error" || page.Events[0].Category != "index" || page.Events[2].Category != "settings" {
		t.Fatalf("events = %+v", page.Events)
	}
}

func TestControlResultsAreLoggedOnceAcrossRestarts(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	state := func(name, body string) {
		t.Helper()
		if err := writePrivateFileAtomic(filepath.Join(a.cfg.stateDir, name), []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	id := func(n string) string { return strings.Repeat(n, 32) }
	state("node-control.status", `{"id":"`+id("a")+`","node":"bitcoin","active":true,"state":"applied","message":"x"}`)
	a.logControlResults()
	if _, err := os.Stat(a.monitor.eventsLog); !os.IsNotExist(err) {
		t.Fatalf("results from before the first run were logged: %v", err)
	}

	state("node-control.status", `{"id":"`+id("b")+`","node":"litecoin","active":false,"state":"applied","message":"x"}`)
	state("port-control.status", `{"id":"`+id("c")+`","state":"rejected","error_code":"port_in_use","port":"bitcoin_rpc"}`)
	state("credential-control.status", `{"id":"`+id("d")+`","node":"bitcoin","state":"applied","message":"x"}`)
	a.logControlResults()
	a.logControlResults()

	restarted := &app{cfg: a.cfg, monitor: a.monitor, log: a.log}
	restarted.logControlResults()
	state("node-control.status", `{"id":"`+id("e")+`","action":"apply_config","state":"applied","restarted":["monero","bitcoin","nope"]}`)
	restarted.logControlResults()
	state("node-control.status", `{"id":"`+id("f")+`","action":"install","node":"tari","state":"failed","error_code":"apply_failed","detail":"compose said rpcpassword=hunter2"}`)
	restarted.logControlResults()

	_, page := eventsRequest(t, handler, "")
	got := make([]string, len(page.Events))
	for i, event := range page.Events {
		got[i] = event.Category + "/" + event.Severity + "/" + event.Node + ": " + event.Text
	}
	want := []string{
		"node/error/tari: Tari: The host could not apply that change.",
		"settings/info/bitcoin: New configuration applied. Restarted Bitcoin, Monero.",
		"security/info/bitcoin: Bitcoin RPC credentials reset.",
		"settings/warning/bitcoin: Selected port for Bitcoin RPC is already in use.",
		"node/info/litecoin: Litecoin deactivated.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events =\n%s", strings.Join(got, "\n"))
	}
}

func TestEventNodePrefersTheEarliestAndLongestName(t *testing.T) {
	for text, want := range map[string]string{
		"Bitcoin Cash activated.":                "bitcoincash",
		"bitcoincash failed to start (x)":        "bitcoincash",
		"Bitcoin activated.":                     "bitcoin",
		"capped monero-wallet at 1 CPUs":         "monero",
		"Moved Litecoin RPC and Bitcoin RPC too": "litecoin",
		"🆕 stack v1.41.0 available":              "",
		"bitcoiners":                             "",
	} {
		if got := eventNode(text); got != want {
			t.Errorf("eventNode(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestVerifyEventDescribesEachOutcome(t *testing.T) {
	match, mismatch := true, false
	for _, tc := range []struct {
		res                verifyResult
		severity, contains string
	}{
		{verifyResult{Height: 9, Match: &match}, "", "verified at height 9"},
		{verifyResult{Height: 9, Match: &mismatch}, "error", "mismatch"},
		{verifyResult{Refusals: []string{"the index has no tip"}}, "warning", "the index has no tip"},
		{verifyResult{Failures: []string{"x"}}, "error", "could not finish"},
	} {
		severity, text := verifyEvent(tc.res)
		if severity != tc.severity || !strings.Contains(text, tc.contains) {
			t.Errorf("verifyEvent(%+v) = %q %q", tc.res, severity, text)
		}
	}
}

func TestEventCategoriesMatchThePanelLabels(t *testing.T) {
	source, err := os.ReadFile("static/events.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, category := range eventCategories {
		if !strings.Contains(string(source), "\n  "+category+": \"") {
			t.Errorf("static/events.js has no label for %q", category)
		}
	}
}

func TestEventsAPISkipsTelegramCopiesOfHostLines(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	writeEventsLog(t, a,
		"2026-11-20T10:00:00Z Host: thermal throttle at 90C — capped bitcoin at 1.5 CPUs",
		"2026-11-20T10:00:00Z Host: Litecoin is not coming up: Docker stopped restarting it after 5 failed starts. Last error: It exited with an error.",
		"2026-11-20T10:00:30Z 🌡 thermal throttle at 90C — capped bitcoin at 1.5 CPUs",
		"2026-11-20T10:01:00Z 🔴 Litecoin is not coming up: Docker stopped restarting it after 5 failed starts. Last error: It exited with an error. | 🌡 thermal throttle at 90C — capped bitcoin at 1.5 CPUs | … and 3 more host events",
		"",
	)
	_, page := eventsRequest(t, handler, "")
	if texts := eventTexts(page.Events); len(texts) != 2 || !strings.HasPrefix(texts[0], "Litecoin is not coming up") || !strings.HasPrefix(texts[1], "thermal throttle") {
		t.Fatalf("events = %q", texts)
	}
}
