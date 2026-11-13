package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const maxRequestBytes = 4096

var hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)
var hex64 = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

type request struct {
	ID             string  `json:"id"`
	Action         string  `json:"action"`
	WalletID       string  `json:"wallet_id"`
	RequestedAt    int64   `json:"requested_at"`
	Name           *string `json:"name,omitempty"`
	PublicSpendKey *string `json:"public_spend_key,omitempty"`
	ViewKey        *string `json:"view_key,omitempty"`
	BirthdayDays   *uint16 `json:"birthday_days,omitempty"`
}

type identity struct {
	WalletID       string `json:"wallet_id"`
	PublicSpendKey string `json:"public_spend_key"`
	BirthdayDays   uint16 `json:"birthday_days"`
}

type status struct {
	ID       string `json:"id"`
	Action   string `json:"action"`
	WalletID string `json:"wallet_id"`
	State    string `json:"state"`
	Message  string `json:"message"`
}

type capability struct {
	Schema          int      `json:"schema"`
	UpdatedAt       int64    `json:"updated_at"`
	Actions         []string `json:"actions"`
	MaxRequestBytes int      `json:"max_request_bytes"`
}

type supervisor struct {
	root       string
	executable string
	config     string
	grpcAddr   string
	timeout    time.Duration
	poll       time.Duration
	child      *exec.Cmd
	childDone  chan error
	renamePath func(string, string) error
	removePath func(string) error
	removeTree func(string) error
}

func main() {
	syscall.Umask(0077)
	password := os.Getenv("TARI_WALLET_PASSWORD")
	if password == "" {
		fmt.Fprintln(os.Stderr, "tari wallet password is required")
		os.Exit(1)
	}
	s := &supervisor{
		root:       "/tari-wallet",
		executable: "/usr/local/bin/minotari_console_wallet",
		config:     "/var/tari/config/config.toml",
		grpcAddr:   "127.0.0.1:18143",
		timeout:    120 * time.Second,
		poll:       time.Second,
	}
	if err := s.run(); err != nil {
		fmt.Fprintln(os.Stderr, sanitize(err.Error()))
		os.Exit(1)
	}
}

func (s *supervisor) run() error {
	if err := s.prepare(); err != nil {
		return err
	}
	if err := s.renderConfig(); err != nil {
		return err
	}
	if err := s.restore(); err != nil {
		s.writeError("", "create", "", err)
		return err
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	lastHeartbeat := time.Time{}
	for {
		select {
		case <-sig:
			s.stopChild()
			return nil
		case err := <-s.childDone:
			s.child = nil
			s.childDone = nil
			stopped := errors.New("wallet process stopped unexpectedly")
			if err != nil {
				stopped = fmt.Errorf("wallet process stopped unexpectedly: %w", err)
			}
			s.writeErrorFromIdentity(stopped)
			return stopped
		case <-ticker.C:
			if time.Since(lastHeartbeat) >= 10*time.Second {
				s.writeCapability()
				lastHeartbeat = time.Now()
			}
			if _, err := os.Stat(s.requestPath()); err == nil {
				s.handleRequest()
			} else if !errors.Is(err, os.ErrNotExist) {
				s.writeError("", "create", "", err)
			}
		}
	}
}

func (s *supervisor) renderConfig() error {
	username := os.Getenv("TARI_WALLET_RPC_USER")
	password := os.Getenv("TARI_WALLET_RPC_PASSWORD")
	if !safeCredential(username, 1) || !safeCredential(password, 8) {
		return errors.New("Tari wallet RPC credentials are invalid")
	}
	template, err := os.ReadFile(s.config)
	if err != nil {
		return errors.New("Tari wallet configuration is unavailable")
	}
	text := string(template)
	if strings.Count(text, "__RPC_USER__") != 1 || strings.Count(text, "__RPC_PASSWORD__") != 1 {
		return errors.New("Tari wallet configuration placeholders are invalid")
	}
	text = strings.Replace(text, "__RPC_USER__", username, 1)
	text = strings.Replace(text, "__RPC_PASSWORD__", password, 1)
	runtimePath := filepath.Join(os.TempDir(), "tari-wallet.toml")
	if err := os.WriteFile(runtimePath, []byte(text), 0600); err != nil {
		return errors.New("could not prepare Tari wallet configuration")
	}
	s.config = runtimePath
	return nil
}

func safeCredential(value string, minLength int) bool {
	if len(value) < minLength || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

func (s *supervisor) prepare() error {
	for _, dir := range []string{s.root, filepath.Join(s.root, "control"), s.privateRoot(), s.archiveRoot()} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("prepare private storage: %w", err)
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return fmt.Errorf("protect private storage: %w", err)
		}
	}
	_ = os.Remove(filepath.Join(s.root, "control", "capability.json"))
	return nil
}

func (s *supervisor) restore() error {
	dbExists := regularFile(s.dbPath())
	identityExists := regularFile(s.identityPath())
	if identityExists {
		id, err := s.readIdentity()
		if err != nil {
			return err
		}
		destination := filepath.Join(s.archiveRoot(), id.WalletID)
		if pathExists(destination) {
			if err := s.recoverInterruptedRemoval(destination, s.dataPath()); err != nil {
				return err
			}
			dbExists = regularFile(s.dbPath())
		}
	}
	if !dbExists && !identityExists {
		return nil
	}
	if !dbExists || !identityExists {
		return errors.New("wallet database and identity marker must both exist")
	}
	id, err := s.readIdentity()
	if err != nil {
		return err
	}
	return s.startRuntime(id)
}

func (s *supervisor) handleRequest() {
	r, err := readRequest(s.requestPath())
	if err != nil {
		s.writeError("", "create", "", err)
		s.clearRequest()
		return
	}
	s.writeStatus(status{ID: r.ID, Action: r.Action, WalletID: r.WalletID, State: "pending", Message: "Request accepted."})
	if r.Action == "create" {
		err = s.create(r)
	} else {
		err = s.remove(r)
	}
	if err != nil {
		s.writeError(r.ID, r.Action, r.WalletID, err)
	}
	s.clearRequest()
}

func readRequest(path string) (request, error) {
	f, err := os.Open(path)
	if err != nil {
		return request{}, fmt.Errorf("read request: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxRequestBytes+1))
	if err != nil {
		return request{}, fmt.Errorf("read request: %w", err)
	}
	if len(b) > maxRequestBytes {
		return request{}, errors.New("request exceeds 4096 bytes")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var r request
	if err := dec.Decode(&r); err != nil {
		return request{}, errors.New("request is invalid")
	}
	if err := ensureJSONEOF(dec); err != nil {
		return request{}, errors.New("request is invalid")
	}
	if err := validateRequest(r); err != nil {
		return request{}, err
	}
	return r, nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	err := dec.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return errors.New("trailing data")
}

func validateRequest(r request) error {
	if !hex32.MatchString(r.ID) || !hex32.MatchString(r.WalletID) || r.RequestedAt <= 0 {
		return errors.New("request identifiers are invalid")
	}
	if r.Action != "create" && r.Action != "remove" {
		return errors.New("request action is invalid")
	}
	if r.Action == "create" {
		if r.ID != r.WalletID || r.PublicSpendKey == nil || r.ViewKey == nil || !hex64.MatchString(*r.PublicSpendKey) || !hex64.MatchString(*r.ViewKey) {
			return errors.New("watch-only wallet keys are invalid")
		}
		if r.Name != nil && (len(*r.Name) > 80 || strings.IndexFunc(*r.Name, func(c rune) bool { return c < 0x20 || c == 0x7f }) >= 0) {
			return errors.New("wallet name is invalid")
		}
	} else if r.Name != nil || r.PublicSpendKey != nil || r.ViewKey != nil || r.BirthdayDays != nil {
		return errors.New("remove request contains create-only fields")
	}
	return nil
}

func (s *supervisor) create(r request) error {
	if s.child != nil || pathExists(s.identityPath()) {
		return errors.New("a Tari watch-only wallet already exists")
	}
	if pathExists(s.dataPath()) {
		if err := s.archiveFailed(r.WalletID); err != nil {
			return fmt.Errorf("archive incomplete Tari wallet before retry: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(s.dbPath()), 0700); err != nil {
		return fmt.Errorf("create wallet storage: %w", err)
	}
	birthday := uint16(0)
	if r.BirthdayDays != nil {
		birthday = *r.BirthdayDays
	}
	cmd, done, err := s.startChild([]string{"--birthday", strconv.FormatUint(uint64(birthday), 10)}, map[string]string{
		"MINOTARI_WALLET_SPEND_KEY":        *r.PublicSpendKey,
		"MINOTARI_WALLET_VIEW_PRIVATE_KEY": *r.ViewKey,
	})
	if err != nil {
		return err
	}
	deadline := time.Now().Add(s.timeout)
	ready := false
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			stopped := errors.New("wallet provisioning stopped")
			if err != nil {
				stopped = fmt.Errorf("wallet provisioning stopped: %w", err)
			}
			return s.provisionError(r.WalletID, stopped)
		case <-time.After(250 * time.Millisecond):
			if regularFile(s.dbPath()) && tcpReady(s.grpcAddr) {
				ready = true
			}
		}
		if ready {
			break
		}
	}
	s.stopCommand(cmd, done)
	if !ready {
		return s.provisionError(r.WalletID, errors.New("wallet provisioning did not become ready within 120 seconds"))
	}
	if err := protectTree(s.dataPath()); err != nil {
		return s.provisionError(r.WalletID, errors.New("wallet database permissions could not be protected"))
	}
	id := identity{WalletID: r.WalletID, PublicSpendKey: strings.ToLower(*r.PublicSpendKey), BirthdayDays: birthday}
	if err := writeJSONAtomic(s.identityPath(), id); err != nil {
		return s.provisionError(r.WalletID, fmt.Errorf("save wallet identity: %w", err))
	}
	if err := s.startRuntime(id); err != nil {
		return err
	}
	s.writeStatus(status{ID: r.ID, Action: r.Action, WalletID: r.WalletID, State: "ready", Message: "Tari watch-only wallet is ready."})
	return nil
}

func (s *supervisor) startRuntime(id identity) error {
	if !regularFile(s.dbPath()) {
		return errors.New("wallet database is missing")
	}
	if err := protectTree(s.dataPath()); err != nil {
		return errors.New("wallet database permissions are unsafe")
	}
	stored, err := s.readIdentity()
	if err != nil {
		return err
	}
	if stored != id {
		return errors.New("wallet identity does not match its private marker")
	}
	cmd, done, err := s.startChild(nil, nil)
	if err != nil {
		return err
	}
	if err := s.waitReady(cmd, done); err != nil {
		return err
	}
	s.child = cmd
	s.childDone = done
	s.writeStatus(status{Action: "create", WalletID: id.WalletID, State: "ready", Message: "Tari watch-only wallet is ready."})
	return nil
}

func (s *supervisor) waitReady(cmd *exec.Cmd, done chan error) error {
	deadline := time.Now().Add(s.timeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			if err == nil {
				return errors.New("wallet process stopped before gRPC became ready")
			}
			return fmt.Errorf("wallet process stopped before gRPC became ready: %w", err)
		case <-time.After(250 * time.Millisecond):
			if tcpReady(s.grpcAddr) {
				return nil
			}
		}
	}
	s.stopCommand(cmd, done)
	return errors.New("wallet gRPC did not become ready within 120 seconds")
}

func (s *supervisor) remove(r request) error {
	id, err := s.readIdentity()
	if err != nil {
		return err
	}
	if id.WalletID != r.WalletID {
		return errors.New("wallet identity does not match the remove request")
	}
	destination := filepath.Join(s.archiveRoot(), r.WalletID)
	if err := s.recoverInterruptedRemoval(destination, s.dataPath()); err != nil {
		return err
	}
	if !regularFile(s.dbPath()) {
		return errors.New("wallet database is missing from active and private archive storage")
	}
	s.stopChild()
	if err := protectTree(s.dataPath()); err != nil {
		return errors.New("wallet database permissions are unsafe")
	}
	if pathExists(destination) {
		return errors.New("wallet archive already exists")
	}
	staging := filepath.Join(s.archiveRoot(), "."+r.WalletID+"-"+r.ID)
	if err := os.Mkdir(staging, 0700); err != nil {
		return fmt.Errorf("create wallet archive: %w", err)
	}
	dataPath := s.dataPath()
	archivedData := filepath.Join(staging, "data")
	archivedIdentity := filepath.Join(staging, "identity.json")
	if err := s.rename(dataPath, archivedData); err != nil {
		cleanupErr := s.removeAll(staging)
		return errors.Join(fmt.Errorf("archive wallet database: %w", err), wrapError("clean incomplete wallet archive", cleanupErr))
	}
	if err := writeJSONAtomic(archivedIdentity, id); err != nil {
		rollbackErr := s.rollbackArchive(archivedData, dataPath, staging)
		return errors.Join(fmt.Errorf("archive wallet identity: %w", err), wrapError("roll back wallet archive", rollbackErr))
	}
	if err := s.rename(staging, destination); err != nil {
		rollbackErr := s.rollbackArchive(archivedData, dataPath, staging)
		return errors.Join(fmt.Errorf("finish wallet archive: %w", err), wrapError("roll back wallet archive", rollbackErr))
	}
	if err := s.removeFile(s.identityPath()); err != nil {
		rollbackErr := s.rollbackArchive(filepath.Join(destination, "data"), dataPath, destination)
		return errors.Join(fmt.Errorf("remove active wallet identity: %w", err), wrapError("roll back wallet archive", rollbackErr))
	}
	s.writeStatus(status{ID: r.ID, Action: r.Action, WalletID: r.WalletID, State: "removed", Message: "Tari watch-only wallet was archived."})
	return nil
}

func (s *supervisor) startChild(extra []string, secrets map[string]string) (*exec.Cmd, chan error, error) {
	args := []string{"--config", s.config, "--base-path", s.dataPath(), "--non-interactive-mode", "--grpc-enabled", "--grpc-address", "/ip4/0.0.0.0/tcp/18143", "-p", "wallet.data_dir=" + s.dataPath(), "-p", "wallet.db_file=" + s.dbPath()}
	args = append(args, extra...)
	cmd := exec.Command(s.executable, args...)
	cmd.Env = childEnvironment(secrets)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, nil, errors.New("could not start Tari wallet")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return cmd, done, nil
}

func childEnvironment(add map[string]string) []string {
	allowed := map[string]bool{"PATH": true, "HOME": true, "USER": true, "LANG": true, "TARI_NETWORK": true, "TARI_TARGET_NETWORK": true}
	env := make([]string, 0, len(allowed)+len(add))
	for _, item := range os.Environ() {
		key := strings.SplitN(item, "=", 2)[0]
		if allowed[key] {
			env = append(env, item)
		}
	}
	env = append(env, "MINOTARI_WALLET_PASSWORD="+os.Getenv("TARI_WALLET_PASSWORD"))
	for key, value := range add {
		env = append(env, key+"="+value)
	}
	return env
}

func (s *supervisor) stopChild() {
	if s.child == nil {
		return
	}
	s.stopCommand(s.child, s.childDone)
	s.child = nil
	s.childDone = nil
}

func (s *supervisor) stopCommand(cmd *exec.Cmd, done chan error) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func (s *supervisor) readIdentity() (identity, error) {
	info, statErr := os.Lstat(s.identityPath())
	if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return identity{}, errors.New("wallet identity marker is missing or not private")
	}
	b, err := os.ReadFile(s.identityPath())
	if err != nil {
		return identity{}, errors.New("wallet identity marker is missing")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var id identity
	if dec.Decode(&id) != nil || ensureJSONEOF(dec) != nil || !hex32.MatchString(id.WalletID) || !hex64.MatchString(id.PublicSpendKey) {
		return identity{}, errors.New("wallet identity marker is invalid")
	}
	id.PublicSpendKey = strings.ToLower(id.PublicSpendKey)
	return id, nil
}

func (s *supervisor) archiveFailed(walletID string) error {
	data := s.dataPath()
	if !pathExists(data) {
		return nil
	}
	if err := protectTree(data); err != nil {
		return fmt.Errorf("protect incomplete wallet database: %w", err)
	}
	destination := filepath.Join(s.archiveRoot(), "failed-"+walletID+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := s.rename(data, destination); err != nil {
		return fmt.Errorf("move incomplete wallet database to private archive: %w", err)
	}
	return nil
}

func (s *supervisor) provisionError(walletID string, cause error) error {
	return errors.Join(cause, wrapError("archive incomplete wallet database", s.archiveFailed(walletID)))
}

func (s *supervisor) rollbackArchive(archivedData, dataPath, cleanupPath string) error {
	var result error
	restored := true
	if pathExists(archivedData) {
		if err := s.rename(archivedData, dataPath); err != nil {
			result = errors.Join(result, fmt.Errorf("restore active wallet database: %w", err))
			restored = false
		}
	}
	if restored {
		if err := s.removeAll(cleanupPath); err != nil {
			result = errors.Join(result, fmt.Errorf("clean incomplete wallet archive: %w", err))
		}
	}
	return result
}

func (s *supervisor) recoverInterruptedRemoval(destination, dataPath string) error {
	if !pathExists(destination) {
		return nil
	}
	archivedData := filepath.Join(destination, "data")
	activeExists := pathExists(dataPath)
	archivedExists := pathExists(archivedData)
	if activeExists && archivedExists {
		return errors.New("wallet database exists in both active and private archive storage")
	}
	if !activeExists && archivedExists {
		if err := s.rename(archivedData, dataPath); err != nil {
			return fmt.Errorf("recover interrupted wallet removal: %w", err)
		}
	}
	if err := s.removeAll(destination); err != nil {
		return fmt.Errorf("clean interrupted wallet removal: %w", err)
	}
	return nil
}

func (s *supervisor) rename(oldPath, newPath string) error {
	if s.renamePath != nil {
		return s.renamePath(oldPath, newPath)
	}
	return os.Rename(oldPath, newPath)
}

func (s *supervisor) removeFile(path string) error {
	if s.removePath != nil {
		return s.removePath(path)
	}
	return os.Remove(path)
}

func (s *supervisor) removeAll(path string) error {
	if s.removeTree != nil {
		return s.removeTree(path)
	}
	return os.RemoveAll(path)
}

func wrapError(message string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

func (s *supervisor) writeErrorFromIdentity(err error) {
	walletID := ""
	if id, readErr := s.readIdentity(); readErr == nil {
		walletID = id.WalletID
	}
	s.writeError("", "create", walletID, err)
}

func (s *supervisor) writeError(id, action, walletID string, err error) {
	s.writeStatus(status{ID: id, Action: action, WalletID: walletID, State: "error", Message: sanitize(err.Error())})
}

func (s *supervisor) writeStatus(value status) {
	_ = writeJSONAtomic(s.statusPath(), value)
}

func (s *supervisor) writeCapability() {
	_ = writeJSONAtomic(filepath.Join(s.root, "control", "capability.json"), capability{
		Schema: 1, UpdatedAt: time.Now().Unix(), Actions: []string{"create", "remove"}, MaxRequestBytes: maxRequestBytes,
	})
}

func writeJSONAtomic(path string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (s *supervisor) clearRequest()        { _ = os.Remove(s.requestPath()) }
func (s *supervisor) requestPath() string  { return filepath.Join(s.root, "control", "request.json") }
func (s *supervisor) statusPath() string   { return filepath.Join(s.root, "control", "status.json") }
func (s *supervisor) identityPath() string { return filepath.Join(s.root, "identity.json") }
func (s *supervisor) privateRoot() string  { return filepath.Join(s.root, "private") }
func (s *supervisor) dataPath() string     { return filepath.Join(s.privateRoot(), "data") }
func (s *supervisor) archiveRoot() string  { return filepath.Join(s.privateRoot(), "archive") }
func (s *supervisor) dbPath() string       { return filepath.Join(s.dataPath(), "wallet.db") }

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func pathExists(path string) bool { _, err := os.Lstat(path); return err == nil }

func protectTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return errors.New("unsupported wallet storage entry")
		}
		if info.IsDir() {
			return os.Chmod(path, 0700)
		}
		return os.Chmod(path, 0600)
	})
}

func tcpReady(address string) bool {
	conn, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func sanitize(message string) string {
	message = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, message)
	if len(message) > 240 {
		message = message[:240]
	}
	return message
}
