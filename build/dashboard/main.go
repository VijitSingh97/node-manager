package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	bitcoinDisabled         bool
	bitcoinActive           bool
	bitcoinActiveSet        bool
	rpcURL                  string
	rpcUser                 string
	rpcPassword             string
	dashboardPassword       string
	dashboardPublicURL      string
	dataDir                 string
	stateDir                string
	version                 string
	port                    string
	settingsPort            string
	moneroEnabled           bool
	moneroActive            bool
	moneroActiveSet         bool
	moneroRPCURL            string
	moneroRPCUser           string
	moneroRPCPassword       string
	moneroDataDir           string
	moneroWalletEnabled     bool
	moneroPublicExample     bool
	moneroWalletRPCURL      string
	moneroWalletRPCUser     string
	moneroWalletRPCPassword string
	moneroWalletPassword    string
	tariWalletEnabled       bool
	tariWalletURL           string
	tariWalletDir           string
	tariWalletRPCUser       string
	tariWalletRPCPassword   string
	bitcoinRPCEndpoint      string
	moneroRPCEndpoint       string
	moneroZMQEndpoint       string
	nodes                   map[string]nodeConfig
}

func envConfig() (config, error) {
	cfg := config{
		bitcoinDisabled:  explicitlyDisabled(os.Getenv("BITCOIN_ENABLED")),
		bitcoinActive:    !explicitlyDisabled(os.Getenv("BITCOIN_ACTIVE")),
		bitcoinActiveSet: true,
		rpcURL:           os.Getenv("RPC_URL"), rpcUser: os.Getenv("RPC_USER"), rpcPassword: os.Getenv("RPC_PASSWORD"),
		dashboardPassword: os.Getenv("DASHBOARD_PASSWORD"), dataDir: os.Getenv("DATA_DIR"), stateDir: os.Getenv("STATE_DIR"), version: strings.TrimSpace(os.Getenv("STACK_VERSION")), port: os.Getenv("PORT"), settingsPort: os.Getenv("SETTINGS_PORT"),
	}
	if cfg.rpcURL == "" {
		cfg.rpcURL = "http://172.29.0.26:8332"
	}
	if cfg.dataDir == "" {
		cfg.dataDir = "/data"
	}
	if cfg.stateDir == "" {
		cfg.stateDir = "/state"
	}
	if cfg.port == "" {
		cfg.port = "8000"
	}
	if err := configureDashboardOrigin(&cfg, os.Getenv("DASHBOARD_PUBLIC_URL")); err != nil {
		return config{}, err
	}
	if cfg.dashboardPublicURL != "" && enabled(os.Getenv("DASHBOARD_ONION")) {
		return config{}, errors.New("DASHBOARD_PUBLIC_URL and DASHBOARD_ONION cannot be enabled together")
	}
	portNumber, err := strconv.Atoi(cfg.port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return config{}, errors.New("PORT must be between 1 and 65535")
	}
	if cfg.settingsPort != "" {
		settingsPortNumber, err := strconv.Atoi(cfg.settingsPort)
		if err != nil || settingsPortNumber < 1 || settingsPortNumber > 65535 {
			return config{}, errors.New("SETTINGS_PORT must be between 1 and 65535")
		}
		if settingsPortNumber == portNumber {
			return config{}, errors.New("SETTINGS_PORT must differ from PORT")
		}
	}
	if cfg.version == "" {
		cfg.version = readVersionFile("VERSION")
		if cfg.version == "" {
			cfg.version = "dev"
		}
	}
	cfg.moneroEnabled = enabled(os.Getenv("MONERO_ENABLED"))
	cfg.moneroActive = !explicitlyDisabled(os.Getenv("MONERO_ACTIVE"))
	cfg.moneroActiveSet = true
	cfg.moneroRPCURL = strings.TrimSpace(os.Getenv("MONERO_RPC_URL"))
	if cfg.moneroRPCURL == "" {
		cfg.moneroRPCURL = "http://172.29.0.30:18085"
	}
	cfg.moneroRPCUser = os.Getenv("MONERO_RPC_USER")
	cfg.moneroRPCPassword = os.Getenv("MONERO_RPC_PASSWORD")
	cfg.moneroDataDir = envOr("MONERO_DATA_DIR", "/data-monero")
	cfg.moneroWalletRPCURL = strings.TrimSpace(os.Getenv("MONERO_WALLET_RPC_URL"))
	if cfg.moneroWalletRPCURL == "" {
		cfg.moneroWalletRPCURL = "http://172.29.0.31:18082"
	}
	cfg.moneroWalletRPCUser = os.Getenv("MONERO_WALLET_RPC_USER")
	cfg.moneroWalletRPCPassword = os.Getenv("MONERO_WALLET_RPC_PASSWORD")
	cfg.moneroWalletPassword = os.Getenv("MONERO_WALLET_PASSWORD")
	walletValues := 0
	for _, value := range []string{cfg.moneroWalletRPCUser, cfg.moneroWalletRPCPassword, cfg.moneroWalletPassword} {
		if value != "" {
			walletValues++
		}
	}
	if walletValues != 0 && walletValues != 3 {
		return config{}, errors.New("MONERO_WALLET_RPC_USER, MONERO_WALLET_RPC_PASSWORD, and MONERO_WALLET_PASSWORD must be set together")
	}
	cfg.moneroWalletEnabled = cfg.moneroEnabled && walletValues == 3
	cfg.moneroPublicExample = cfg.moneroWalletEnabled && !explicitlyDisabled(os.Getenv("MONERO_PUBLIC_EXAMPLE"))
	var exposureErr error
	if !cfg.bitcoinDisabled {
		if cfg.bitcoinRPCEndpoint, exposureErr = exposedLANEndpoint("BITCOIN_RPC_EXPOSE", "BITCOIN_RPC_BIND_IP", "BITCOIN_RPC_PORT", "8332"); exposureErr != nil {
			return config{}, exposureErr
		}
	}
	if cfg.moneroEnabled {
		if cfg.moneroRPCEndpoint, exposureErr = exposedLANEndpoint("MONERO_RPC_EXPOSE", "MONERO_RPC_BIND_IP", "MONERO_RPC_PORT", "18081"); exposureErr != nil {
			return config{}, exposureErr
		}
		if cfg.moneroZMQEndpoint, exposureErr = exposedLANEndpoint("MONERO_ZMQ_EXPOSE", "MONERO_ZMQ_BIND_IP", "MONERO_ZMQ_PORT", "18083"); exposureErr != nil {
			return config{}, exposureErr
		}
	}
	if err := loadAdditionalNodes(&cfg); err != nil {
		return config{}, err
	}
	cfg.tariWalletEnabled = cfg.nodes[nodeTari].Enabled && enabled(os.Getenv("TARI_WALLET_ENABLED"))
	cfg.tariWalletURL = strings.TrimSpace(envOr("TARI_WALLET_GRPC_URL", "http://172.29.0.37:18143"))
	cfg.tariWalletDir = envOr("TARI_WALLET_DIR", "/tari-wallet")
	cfg.tariWalletRPCUser = os.Getenv("TARI_WALLET_RPC_USER")
	cfg.tariWalletRPCPassword = os.Getenv("TARI_WALLET_RPC_PASSWORD")
	if cfg.tariWalletEnabled {
		if err := validateInternalHTTPURL("TARI_WALLET_GRPC_URL", cfg.tariWalletURL); err != nil {
			return config{}, err
		}
		if !filepath.IsAbs(cfg.tariWalletDir) {
			return config{}, errors.New("TARI_WALLET_DIR must be an absolute path")
		}
		if cfg.tariWalletRPCUser == "" || cfg.tariWalletRPCPassword == "" {
			return config{}, errors.New("TARI_WALLET_RPC_USER and TARI_WALLET_RPC_PASSWORD are required when TARI_WALLET_ENABLED is true")
		}
	}
	if (cfg.tariWalletRPCUser == "") != (cfg.tariWalletRPCPassword == "") {
		return config{}, errors.New("TARI_WALLET_RPC_USER and TARI_WALLET_RPC_PASSWORD must be set together")
	}
	if node := cfg.nodes[nodeZcash]; node.Enabled && node.RPCEndpoint != "" && (cfg.port == "18232" || cfg.settingsPort == "18232") {
		return config{}, errors.New("PORT and SETTINGS_PORT must differ from the internal Zcash proxy port 18232")
	}
	if cfg.moneroWalletEnabled {
		if err := validateInternalHTTPURL("MONERO_WALLET_RPC_URL", cfg.moneroWalletRPCURL); err != nil {
			return config{}, err
		}
	}
	if cfg.moneroEnabled {
		if err := validateInternalHTTPURL("MONERO_RPC_URL", cfg.moneroRPCURL); err != nil {
			return config{}, err
		}
		if cfg.moneroRPCUser == "" || cfg.moneroRPCPassword == "" {
			return config{}, errors.New("MONERO_RPC_USER and MONERO_RPC_PASSWORD are required when MONERO_ENABLED is true")
		}
	}
	if !cfg.bitcoinDisabled && (cfg.rpcUser == "" || cfg.rpcPassword == "") {
		return config{}, errors.New("RPC_USER and RPC_PASSWORD are required")
	}
	if !cfg.bitcoinDisabled {
		if err := validateInternalHTTPURL("RPC_URL", cfg.rpcURL); err != nil {
			return config{}, err
		}
	}
	return cfg, nil
}

func exposedLANEndpoint(enabledName, addressName, portName, defaultPort string) (string, error) {
	if !enabled(os.Getenv(enabledName)) {
		return "", nil
	}
	address := strings.TrimSpace(os.Getenv(addressName))
	ip := net.ParseIP(address)
	if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
		return "", errors.New(addressName + " must be a private or loopback IP address when exposure is enabled")
	}
	port := strings.TrimSpace(os.Getenv(portName))
	if port == "" {
		port = defaultPort
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", errors.New(portName + " must be between 1 and 65535")
	}
	return net.JoinHostPort(address, port), nil
}

func explicitlyDisabled(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "0" || value == "false"
}

func serveHTTPServers(ctx context.Context, servers []*http.Server) error {
	listeners := make([]net.Listener, 0, len(servers))
	for _, server := range servers {
		listener, err := net.Listen("tcp", server.Addr)
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			return err
		}
		listeners = append(listeners, listener)
	}

	errorsByServer := make(chan error, len(servers))
	for i, server := range servers {
		go func(server *http.Server, listener net.Listener) {
			errorsByServer <- server.Serve(listener)
		}(server, listeners[i])
	}

	var firstErr error
	consumed := false
	select {
	case <-ctx.Done():
	case err := <-errorsByServer:
		consumed = true
		if !errors.Is(err, http.ErrServerClosed) {
			firstErr = err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, server := range servers {
		_ = server.Shutdown(shutdownCtx)
	}
	remaining := len(servers)
	if consumed {
		remaining--
	}
	for range remaining {
		if err := <-errorsByServer; firstErr == nil && !errors.Is(err, http.ErrServerClosed) {
			firstErr = err
		}
	}
	return firstErr
}

func runDashboardServers(ctx context.Context, stop func(), a *app, servers []*http.Server) error {
	backgroundDone := a.startBackground(ctx)
	err := serveHTTPServers(ctx, servers)
	stop()
	<-backgroundDone
	return err
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		client := &http.Client{Timeout: 5 * time.Second}
		response, err := client.Get("http://127.0.0.1:" + envOr("PORT", "8000") + "/healthz")
		if err != nil {
			os.Exit(1)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}
	cfg, err := envConfig()
	if err != nil {
		log.Fatal(err)
	}
	logger := log.New(os.Stderr, "go-dashboard: ", log.LstdFlags)
	rpc := &rpcClient{url: cfg.rpcURL, user: cfg.rpcUser, password: cfg.rpcPassword, client: &http.Client{Timeout: 3 * time.Second}}
	a, err := newApp(cfg, rpc, "index.html", "static", logger)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	handler := a.routes()
	servers := []*http.Server{{Addr: ":" + cfg.port, Handler: handler, MaxHeaderBytes: 16 << 10, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 6 * time.Minute, IdleTimeout: 60 * time.Second}}
	if cfg.settingsPort != "" {
		servers = append(servers, &http.Server{Addr: ":" + cfg.settingsPort, Handler: handler, MaxHeaderBytes: 16 << 10, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 6 * time.Minute, IdleTimeout: 60 * time.Second})
		logger.Printf("onion settings listener enabled on :%s", cfg.settingsPort)
	}
	if node, ok := cfg.nodes[nodeZcash]; ok && node.Enabled && node.RPCEndpoint != "" {
		servers = append(servers, &http.Server{Addr: ":18232", Handler: a.zcashRPCProxy(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second})
		logger.Print("authenticated Zcash read-only RPC proxy enabled on :18232")
	}
	logger.Printf("dashboard listening on :%s", cfg.port)
	if err := runDashboardServers(ctx, stop, a, servers); err != nil {
		log.Fatal(err)
	}
}
