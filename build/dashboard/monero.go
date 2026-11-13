package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxMoneroRPCBody = 1 << 20

var errMoneroRemoteUnknown = errors.New("Monero wallet RPC result is unknown")

type moneroStatus struct {
	Enabled               bool     `json:"enabled"`
	Available             bool     `json:"available"`
	Synced                bool     `json:"synced"`
	Height                uint64   `json:"height"`
	TargetHeight          uint64   `json:"target_height"`
	SyncPercent           float64  `json:"sync_percent"`
	Version               string   `json:"version"`
	UptimeSeconds         uint64   `json:"uptime_seconds"`
	UptimeAvailable       bool     `json:"uptime_available"`
	IncomingConnections   uint64   `json:"incoming_connections"`
	OutgoingConnections   uint64   `json:"outgoing_connections"`
	Pruned                bool     `json:"pruned"`
	DatabaseSizeBytes     uint64   `json:"database_size_bytes"`
	DatabaseSizeAvailable bool     `json:"database_size_available"`
	MempoolTxs            uint64   `json:"mempool_txs"`
	MempoolTxsAvailable   bool     `json:"mempool_txs_available"`
	MempoolBytes          uint64   `json:"mempool_bytes"`
	MempoolBytesAvailable bool     `json:"mempool_bytes_available"`
	Difficulty            uint64   `json:"difficulty"`
	DifficultyAvailable   bool     `json:"difficulty_available"`
	NetworkHashrate       float64  `json:"network_hashrate"`
	HashrateAvailable     bool     `json:"hashrate_available"`
	LatestBlockTimestamp  uint64   `json:"latest_block_timestamp"`
	LatestBlockAgeSeconds uint64   `json:"latest_block_age_seconds"`
	LatestBlockAvailable  bool     `json:"latest_block_available"`
	FeeEstimates          []uint64 `json:"fee_estimates_atomic_per_byte"`
	FeesAvailable         bool     `json:"fees_available"`
	DiskTotalBytes        uint64   `json:"disk_total_bytes"`
	DiskFreeBytes         uint64   `json:"disk_free_bytes"`
	DiskAvailable         bool     `json:"disk_available"`
	RPCExposureEnabled    bool     `json:"rpc_exposure_enabled"`
	RPCEndpoint           string   `json:"rpc_endpoint"`
	ZMQExposureEnabled    bool     `json:"zmq_exposure_enabled"`
	ZMQEndpoint           string   `json:"zmq_endpoint"`
	Error                 string   `json:"error"`
	UpdatedAt             int64    `json:"updated_at"`
}

type moneroInfo struct {
	Synchronized             bool    `json:"synchronized"`
	BusySyncing              bool    `json:"busy_syncing"`
	Height                   uint64  `json:"height"`
	TargetHeight             uint64  `json:"target_height"`
	Version                  string  `json:"version"`
	StartTime                uint64  `json:"start_time"`
	IncomingConnectionsCount uint64  `json:"incoming_connections_count"`
	OutgoingConnectionsCount uint64  `json:"outgoing_connections_count"`
	PruningSeed              uint64  `json:"pruning_seed"`
	DatabaseSize             uint64  `json:"database_size"`
	Difficulty               uint64  `json:"difficulty"`
	Target                   uint64  `json:"target"`
	TxPoolSize               *uint64 `json:"tx_pool_size"`
}

type moneroBlockHeader struct {
	Timestamp uint64 `json:"timestamp"`
}
type moneroLastBlock struct {
	BlockHeader moneroBlockHeader `json:"block_header"`
}
type moneroPoolStats struct {
	Status    string `json:"status"`
	PoolStats *struct {
		BytesTotal *uint64 `json:"bytes_total"`
		TxsTotal   *uint64 `json:"txs_total"`
	} `json:"pool_stats"`
}

type moneroFeeEstimate struct {
	Fees   []uint64 `json:"fees"`
	Status string   `json:"status"`
}

type moneroRPC struct {
	url      string
	user     string
	password string
	client   *http.Client
	maxBody  int64
}

func (a *app) apiMonero(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	status := moneroStatus{
		Enabled:            a.cfg.moneroEnabled,
		RPCExposureEnabled: a.cfg.moneroRPCEndpoint != "", RPCEndpoint: a.cfg.moneroRPCEndpoint,
		ZMQExposureEnabled: a.cfg.moneroZMQEndpoint != "", ZMQEndpoint: a.cfg.moneroZMQEndpoint,
	}
	if !a.cfg.moneroEnabled {
		writeJSON(w, status)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rpc := moneroRPC{
		url:      a.cfg.moneroRPCURL,
		user:     a.cfg.moneroRPCUser,
		password: a.cfg.moneroRPCPassword,
		client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
	var info moneroInfo
	if err := rpc.call(ctx, "get_info", &info); err != nil {
		status.Error = "Monero RPC unavailable"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, status)
		return
	}
	status.Available = true
	status.UpdatedAt = time.Now().Unix()
	status.Synced = info.Synchronized && !info.BusySyncing
	status.Height = info.Height
	status.TargetHeight = info.TargetHeight
	status.Version = info.Version
	status.IncomingConnections = info.IncomingConnectionsCount
	status.OutgoingConnections = info.OutgoingConnectionsCount
	status.Pruned = info.PruningSeed != 0
	status.DatabaseSizeBytes = info.DatabaseSize
	status.DatabaseSizeAvailable = info.DatabaseSize > 0
	if info.TxPoolSize != nil {
		status.MempoolTxs, status.MempoolTxsAvailable = *info.TxPoolSize, true
	}
	status.Difficulty = info.Difficulty
	status.DifficultyAvailable = info.Difficulty > 0
	if info.Difficulty > 0 && info.Target > 0 {
		status.NetworkHashrate = float64(info.Difficulty) / float64(info.Target)
		status.HashrateAvailable = true
	}
	if disk := diskUsage(a.cfg.moneroDataDir); disk.Total > 0 {
		status.DiskTotalBytes, status.DiskFreeBytes, status.DiskAvailable = disk.Total, disk.Free, true
	}
	var last moneroLastBlock
	if rpc.call(ctx, "get_last_block_header", &last) == nil && last.BlockHeader.Timestamp > 0 {
		status.LatestBlockTimestamp, status.LatestBlockAvailable = last.BlockHeader.Timestamp, true
		now := uint64(time.Now().Unix())
		if now > last.BlockHeader.Timestamp {
			status.LatestBlockAgeSeconds = now - last.BlockHeader.Timestamp
		}
	}
	var pool moneroPoolStats
	if rpc.callPath(ctx, "/get_transaction_pool_stats", &pool) == nil && strings.EqualFold(pool.Status, "OK") && pool.PoolStats != nil && pool.PoolStats.TxsTotal != nil && pool.PoolStats.BytesTotal != nil {
		status.MempoolTxs, status.MempoolBytes = *pool.PoolStats.TxsTotal, *pool.PoolStats.BytesTotal
		status.MempoolTxsAvailable, status.MempoolBytesAvailable = true, true
	}
	if fees, err := a.fetchMoneroFeeEstimate(ctx); err == nil {
		status.FeeEstimates, status.FeesAvailable = fees, true
	}
	if info.StartTime > 0 && uint64(time.Now().Unix()) > info.StartTime {
		status.UptimeSeconds = uint64(time.Now().Unix()) - info.StartTime
		status.UptimeAvailable = true
	}
	target := info.TargetHeight
	if target < info.Height {
		target = info.Height
	}
	if status.Synced {
		status.SyncPercent = 100
	} else if target > 0 {
		status.SyncPercent = float64(info.Height) / float64(target) * 100
		if status.SyncPercent > 100 {
			status.SyncPercent = 100
		}
	}
	writeJSON(w, status)
}

func (m moneroRPC) call(ctx context.Context, method string, out any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "dashboard", "method": method})
	if err != nil {
		return err
	}
	responseBody, err := m.do(ctx, http.MethodPost, "/json_rpc", body)
	if err != nil {
		return err
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return errors.New("invalid Monero RPC response")
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return errors.New("Monero RPC error")
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return errors.New("invalid Monero RPC result")
	}
	return nil
}

func (m moneroRPC) callPath(ctx context.Context, path string, out any) error {
	responseBody, err := m.do(ctx, http.MethodPost, path, []byte("{}"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return errors.New("invalid Monero RPC response")
	}
	return nil
}

func (m moneroRPC) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	endpoint, err := url.Parse(m.url)
	if err != nil {
		return nil, errors.New("invalid Monero RPC URL")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	request := func(authorization string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		return m.client.Do(req)
	}
	response, err := request("")
	if err != nil {
		return nil, err
	}
	// A second 401 means the nonce went stale between challenge and use; RFC 7616 says re-handshake.
	for attempt := 0; attempt < 3 && response.StatusCode == http.StatusUnauthorized; attempt++ {
		challenge := digestChallenge(response.Header.Values("WWW-Authenticate"))
		response.Body.Close()
		authorization, err := digestAuthorization(challenge, m.user, m.password, method, endpoint.RequestURI(), digestNonceCount(m.url+"|"+m.user, challenge["nonce"]))
		if err != nil {
			return nil, err
		}
		response, err = request(authorization)
		if err != nil {
			return nil, err
		}
	}
	return readMoneroRPCResponse(response, m.maxBody)
}

func (m moneroRPC) doDigest(ctx context.Context, method, path string, body, probeBody []byte) ([]byte, error) {
	endpoint, err := url.Parse(m.url)
	if err != nil {
		return nil, errors.New("invalid Monero RPC URL")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	request := func(payload []byte, authorization string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		return m.client.Do(req)
	}
	probe, err := request(probeBody, "")
	if err != nil {
		if probe != nil && probe.Body != nil {
			probe.Body.Close()
		}
		return nil, err
	}
	if probe.StatusCode != http.StatusUnauthorized {
		probe.Body.Close()
		return nil, errors.New("Monero wallet RPC did not require Digest authentication")
	}
	challenge := digestChallenge(probe.Header.Values("WWW-Authenticate"))
	probe.Body.Close()
	authorization, err := digestAuthorization(challenge, m.user, m.password, method, endpoint.RequestURI(), digestNonceCount(m.url+"|"+m.user, challenge["nonce"]))
	if err != nil {
		return nil, err
	}
	response, err := request(body, authorization)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, fmt.Errorf("%w: authenticated request failed", errMoneroRemoteUnknown)
	}
	if response.StatusCode == http.StatusUnauthorized {
		challenge = digestChallenge(response.Header.Values("WWW-Authenticate"))
		response.Body.Close()
		authorization, err = digestAuthorization(challenge, m.user, m.password, method, endpoint.RequestURI(), digestNonceCount(m.url+"|"+m.user, challenge["nonce"]))
		if err != nil {
			return nil, err
		}
		response, err = request(body, authorization)
		if err != nil {
			if response != nil && response.Body != nil {
				response.Body.Close()
			}
			return nil, fmt.Errorf("%w: authenticated request failed", errMoneroRemoteUnknown)
		}
	}
	responseBody, err := readMoneroRPCResponse(response, m.maxBody)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errMoneroRemoteUnknown, err)
	}
	return responseBody, nil
}

func readMoneroRPCResponse(response *http.Response, limit int64) ([]byte, error) {
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Monero RPC returned HTTP %d", response.StatusCode)
	}
	if limit <= 0 {
		limit = maxMoneroRPCBody
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, errors.New("read Monero RPC response")
	}
	if int64(len(responseBody)) > limit {
		return nil, errors.New("Monero RPC response too large")
	}
	return responseBody, nil
}

func digestChallenge(values []string) map[string]string {
	for _, value := range values {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "digest ") {
			return parseDigestParameters(strings.TrimSpace(value)[len("Digest "):])
		}
	}
	return nil
}

func parseDigestParameters(value string) map[string]string {
	parameters := make(map[string]string)
	for len(value) > 0 {
		value = strings.TrimLeft(value, " ,")
		equals := strings.IndexByte(value, '=')
		if equals <= 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(value[:equals]))
		value = strings.TrimSpace(value[equals+1:])
		var parsed string
		if strings.HasPrefix(value, `"`) {
			value = value[1:]
			var builder strings.Builder
			escaped := false
			index := 0
			for ; index < len(value); index++ {
				character := value[index]
				if escaped {
					builder.WriteByte(character)
					escaped = false
					continue
				}
				if character == '\\' {
					escaped = true
					continue
				}
				if character == '"' {
					break
				}
				builder.WriteByte(character)
			}
			if index >= len(value) {
				return nil
			}
			parsed = builder.String()
			value = value[index+1:]
		} else {
			comma := strings.IndexByte(value, ',')
			if comma < 0 {
				parsed, value = strings.TrimSpace(value), ""
			} else {
				parsed, value = strings.TrimSpace(value[:comma]), value[comma+1:]
			}
		}
		parameters[key] = parsed
	}
	return parameters
}

var digestNonceCounts sync.Map

// Digest replay protection: a server nonce's nc must increase across every
// request that uses it, including from concurrent callers.
func digestNonceCount(key, nonce string) string {
	entry, _ := digestNonceCounts.LoadOrStore(key, &digestCounter{})
	counter := entry.(*digestCounter)
	counter.mu.Lock()
	defer counter.mu.Unlock()
	if counter.nonce != nonce {
		counter.nonce = nonce
		counter.count = 0
	}
	counter.count++
	return fmt.Sprintf("%08x", counter.count)
}

type digestCounter struct {
	mu    sync.Mutex
	nonce string
	count uint32
}

func digestAuthorization(challenge map[string]string, username, password, method, uri, nonceCount string) (string, error) {
	realm, nonce := challenge["realm"], challenge["nonce"]
	if realm == "" || nonce == "" || username == "" {
		return "", errors.New("invalid Monero RPC digest challenge")
	}
	algorithm := strings.ToUpper(challenge["algorithm"])
	if algorithm == "" {
		algorithm = "MD5"
	}
	baseAlgorithm := strings.TrimSuffix(algorithm, "-SESS")
	var digest func(string) string
	switch baseAlgorithm {
	case "MD5":
		digest = func(value string) string {
			sum := md5.Sum([]byte(value))
			return hex.EncodeToString(sum[:])
		}
	case "SHA-256":
		digest = func(value string) string {
			sum := sha256.Sum256([]byte(value))
			return hex.EncodeToString(sum[:])
		}
	default:
		return "", errors.New("unsupported Monero RPC digest algorithm")
	}
	cnonceBytes := make([]byte, 16)
	if _, err := rand.Read(cnonceBytes); err != nil {
		return "", errors.New("create Monero RPC digest nonce")
	}
	cnonce := hex.EncodeToString(cnonceBytes)
	ha1 := digest(username + ":" + realm + ":" + password)
	if strings.HasSuffix(algorithm, "-SESS") {
		ha1 = digest(ha1 + ":" + nonce + ":" + cnonce)
	}
	ha2 := digest(method + ":" + uri)
	qop := ""
	for _, option := range strings.Split(challenge["qop"], ",") {
		if strings.EqualFold(strings.TrimSpace(option), "auth") {
			qop = "auth"
			break
		}
	}
	var response string
	if challenge["qop"] != "" {
		if qop == "" {
			return "", errors.New("unsupported Monero RPC digest qop")
		}
		response = digest(strings.Join([]string{ha1, nonce, nonceCount, cnonce, qop, ha2}, ":"))
	} else {
		response = digest(strings.Join([]string{ha1, nonce, ha2}, ":"))
	}
	quote := func(value string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"` }
	parts := []string{
		"username=" + quote(username), "realm=" + quote(realm), "nonce=" + quote(nonce),
		"uri=" + quote(uri), "response=" + quote(response), "algorithm=" + algorithm,
	}
	if opaque := challenge["opaque"]; opaque != "" {
		parts = append(parts, "opaque="+quote(opaque))
	}
	if qop != "" {
		parts = append(parts, "qop="+qop, "nc="+nonceCount, "cnonce="+quote(cnonce))
	}
	return "Digest " + strings.Join(parts, ", "), nil
}
