package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func (m *Monitor) sendTelegram(ctx context.Context, text string) bool {
	m.logEvent(text)
	settings := m.notificationSettings()
	if !settings.TelegramEnabled || settings.TelegramBotToken == "" || settings.TelegramChatID == "" {
		return false
	}
	payload, err := json.Marshal(map[string]string{
		"chat_id": settings.TelegramChatID,
		"text":    "[" + m.nodeName + "] " + text,
	})
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+settings.TelegramBotToken+"/sendMessage", bytes.NewReader(payload))
	if err != nil {
		m.writeOutput("Telegram send failed")
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		m.writeOutput("Telegram send failed")
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		m.writeOutput("Telegram send failed")
		return false
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil || !result.OK {
		m.writeOutput("Telegram send failed")
		return false
	}
	return true
}

func (m *Monitor) sendOperationalTelegram(ctx context.Context, text string) bool {
	if !m.notificationSettings().TelegramEnabled {
		m.logEvent(text)
		return true
	}
	return m.sendTelegram(ctx, text)
}

func (m *Monitor) sendOperationalAlert(ctx context.Context, enabled bool, text string) bool {
	if !enabled {
		m.logEvent(text)
		return true
	}
	return m.sendOperationalTelegram(ctx, text)
}

func (m *Monitor) pingHealthchecks(ctx context.Context) bool {
	settings := m.notificationSettings()
	if !settings.HealthchecksEnabled || settings.HealthchecksURL == "" {
		return true
	}
	endpoint, err := url.Parse(settings.HealthchecksURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil {
		m.logEvent("healthchecks ping failed: HTTPS URL required")
		return false
	}
	reason := "request failed"
	for attempt := 0; attempt < 3; attempt++ {
		client := m.client
		if attempt > 0 {
			client = m.freshTorClient()
		}
		ok := func() bool {
			attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, endpoint.String(), nil)
			if err != nil {
				return false
			}
			resp, err := client.Do(req)
			if err != nil {
				return false
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				reason = fmt.Sprintf("HTTP %d", resp.StatusCode)
				return false
			}
			return true
		}()
		if attempt > 0 {
			client.CloseIdleConnections()
		}
		if ok {
			return true
		}
		if ctx.Err() != nil {
			break
		}
	}
	m.logEvent("healthchecks ping failed: " + reason)
	return false
}
func (m *Monitor) forwardHostEvents(ctx context.Context) {
	m.logMu.Lock()
	start := m.hostEventsOffset
	var chunk []byte
	size := int64(0)
	if stat, err := os.Stat(m.eventsLog); err == nil {
		size = stat.Size()
	}
	if size < m.hostEventsOffset {
		m.hostEventsOffset = size
	}
	if size > m.hostEventsOffset {
		if f, err := os.Open(m.eventsLog); err == nil {
			data := make([]byte, size-m.hostEventsOffset)
			if _, err := f.ReadAt(data, m.hostEventsOffset); err == nil {
				if end := bytes.LastIndexByte(data, '\n'); end >= 0 {
					chunk = data[:end+1]
					m.hostEventsOffset += int64(end + 1)
				}
			}
			f.Close()
		}
	}
	m.logMu.Unlock()
	settings := m.notificationSettings()
	telegramOn := settings.TelegramEnabled && settings.TelegramBotToken != "" && settings.TelegramChatID != ""
	consumed := 0
	failed := false
	for rest := string(chunk); rest != ""; {
		line, more, _ := strings.Cut(rest, "\n")
		rest = more
		if _, text, found := strings.Cut(line, " "); found && strings.HasPrefix(text, "Host: ") && telegramOn {
			if !m.sendTelegram(ctx, "🌡 "+strings.TrimPrefix(text, "Host: ")) {
				failed = true
				break
			}
		}
		consumed += len(line) + 1
	}
	m.logMu.Lock()
	if failed {
		m.hostEventsOffset -= int64(len(chunk) - consumed)
		if m.hostEventsOffset < 0 {
			m.hostEventsOffset = 0
		}
	}
	moved := m.hostEventsOffset != start
	m.logMu.Unlock()
	if moved {
		m.saveState()
	}
}

func (m *Monitor) logEvent(text string) {
	m.logMu.Lock()
	defer m.logMu.Unlock()
	m.writeOutput(text)
	if stat, err := os.Stat(m.eventsLog); err == nil && stat.Size() > eventLogCap {
		if err := rotateEventLog(m.eventsLog); err != nil {
			m.writeOutput("Event log write failed")
			return
		}
		if rotated, err := os.Stat(m.eventsLog); err == nil {
			if removed := stat.Size() - rotated.Size(); removed > 0 {
				if m.hostEventsOffset > removed {
					m.hostEventsOffset -= removed
				} else {
					m.hostEventsOffset = 0
				}
			}
		}
	}
	f, err := os.OpenFile(m.eventsLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		m.writeOutput("Event log write failed")
		return
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%s %s\n", m.now().UTC().Format(time.RFC3339), text); err != nil {
		m.writeOutput("Event log write failed")
	}
}

func rotateEventLog(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), eventLogCap+1)
	lines := make([]string, 0, 65536)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	closeErr := f.Close()
	if err := scanner.Err(); err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	lines = lines[len(lines)/2:]
	var content strings.Builder
	for _, line := range lines {
		content.WriteString(line)
		content.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(content.String()), 0o600)
}

func (m *Monitor) writeOutput(text string) {
	if m.output != nil {
		fmt.Fprintln(m.output, text)
	}
}
