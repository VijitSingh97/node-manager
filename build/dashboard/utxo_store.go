package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
)

const (
	utxoSchemaVersion   = 1
	utxoPebbleFormat    = pebble.FormatFlushableIngest
	utxoJournalDepth    = 144
	utxoPrefixLen       = 16
	utxoKeyLen          = utxoPrefixLen + 32 + 4
	utxoValueLen        = 12
	utxoMaxScriptLen    = 10000
	utxoBlockCacheBytes = 64 << 20
	utxoMemTableBytes   = 16 << 20
	utxoCompactions     = 1
	utxoIngestTmpDir    = "ingest-tmp"
)

var (
	utxoMetaKey = []byte("meta")

	errUtxoInvalid       = errors.New("utxo index is invalid")
	errUtxoClosed        = errors.New("utxo store is closed")
	errUtxoBroken        = errors.New("utxo store hit a fatal storage error, reopen required")
	errUtxoNotContiguous = errors.New("block does not extend the indexed tip")
	errUtxoMissingCoin   = errors.New("spent coin is not in the index")
	errUtxoNoTip         = errors.New("index has no tip")
	errUtxoNoUndo        = errors.New("undo record is not available")
	errUtxoNotEmpty      = errors.New("utxo store is not empty")
)

type utxoOutpoint struct {
	Txid [32]byte
	Vout uint32
}

type utxoCreate struct {
	utxoOutpoint
	Script []byte
	Amount uint64
}

type utxoSpend struct {
	utxoOutpoint
	Script []byte
}

type utxoBlock struct {
	Height   uint32
	Hash     [32]byte
	PrevHash [32]byte
	Creates  []utxoCreate
	Spends   []utxoSpend
}

type utxoCoin struct {
	ScriptHash [utxoPrefixLen]byte
	utxoOutpoint
	Amount uint64
	Height uint32
}

type utxoMeta struct {
	Schema       int    `json:"schema"`
	PebbleFormat int    `json:"pebble_format"`
	HasTip       bool   `json:"has_tip"`
	TipHeight    uint32 `json:"tip_height"`
	TipHash      string `json:"tip_hash"`
	Ingesting    bool   `json:"ingesting,omitempty"`
}

type utxoStore struct {
	mu            sync.RWMutex
	db            *pebble.DB
	meta          utxoMeta
	invalidReason string
	beforeCommit  func() error
	beforeIngest  func() error
	dir           string
	fs            vfs.FS
	discard       bool
	fatal         atomic.Pointer[string]
}

type utxoQuietLogger struct{ s *utxoStore }

func (utxoQuietLogger) Infof(string, ...interface{}) {}

// Pebble also calls this from background goroutines, so it records instead of panicking.
func (l utxoQuietLogger) Fatalf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	l.s.fatal.CompareAndSwap(nil, &msg)
}

func (s *utxoStore) brokenError() error {
	if m := s.fatal.Load(); m != nil {
		return fmt.Errorf("%w: %s", errUtxoBroken, *m)
	}
	return nil
}

// Pebble returns nil from Commit after a fatal WAL failure, so the flag is the only signal.
func (s *utxoStore) commit(b *pebble.Batch) error {
	err := b.Commit(pebble.Sync)
	if berr := s.brokenError(); berr != nil {
		s.abandon()
		return berr
	}
	return err
}

func (s *utxoStore) abandon() {
	defer func() { _ = recover() }()
	_ = s.db.Close()
	s.db = nil
}

func utxoScriptHash(script []byte) [utxoPrefixLen]byte {
	sum := sha256.Sum256(script)
	var h [utxoPrefixLen]byte
	copy(h[:], sum[:utxoPrefixLen])
	return h
}

func utxoSpendable(script []byte) bool {
	return len(script) <= utxoMaxScriptLen && (len(script) == 0 || script[0] != 0x6a)
}

func utxoCoinKey(h [utxoPrefixLen]byte, o utxoOutpoint) []byte {
	k := make([]byte, 0, utxoKeyLen)
	k = append(k, h[:]...)
	k = append(k, o.Txid[:]...)
	return binary.BigEndian.AppendUint32(k, o.Vout)
}

func utxoCoinValue(amount uint64, height uint32) []byte {
	v := binary.BigEndian.AppendUint64(make([]byte, 0, utxoValueLen), amount)
	return binary.BigEndian.AppendUint32(v, height)
}

// Undo keys are 8 bytes and the meta key 4, so neither can fall inside a 16-byte coin prefix range.
func utxoUndoKey(height uint32) []byte {
	return binary.BigEndian.AppendUint32([]byte("undo"), height)
}

func openUtxoStore(dir string) (*utxoStore, error) {
	return openUtxoStoreFS(dir, nil)
}

func openUtxoStoreFS(dir string, fs vfs.FS) (*utxoStore, error) {
	if fs == nil {
		fs = vfs.Default
	}
	s := &utxoStore{dir: dir, fs: fs}
	opts := &pebble.Options{
		Cache:                    pebble.NewCache(utxoBlockCacheBytes),
		MemTableSize:             utxoMemTableBytes,
		MaxConcurrentCompactions: func() int { return utxoCompactions },
		FormatMajorVersion:       utxoPebbleFormat,
		Logger:                   utxoQuietLogger{s: s},
		FS:                       fs,
	}
	defer opts.Cache.Unref()
	db, err := pebble.Open(dir, opts)
	if err != nil {
		return nil, fmt.Errorf("open utxo store: %w", err)
	}
	s.db = db
	if err := s.loadMeta(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if s.discard {
		_ = db.Close()
		if err := clearUtxoDir(fs, dir); err != nil {
			return nil, fmt.Errorf("discard interrupted utxo ingest: %w", err)
		}
		return openUtxoStoreFS(dir, fs)
	}
	if s.invalidReason != "" {
		_ = db.Close()
		s.db = nil
		return s, nil
	}
	if err := fs.RemoveAll(fs.PathJoin(dir, utxoIngestTmpDir)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("clear utxo ingest temp: %w", err)
	}
	return s, nil
}

func (s *utxoStore) loadMeta() error {
	raw, closer, err := s.db.Get(utxoMetaKey)
	if errors.Is(err, pebble.ErrNotFound) {
		return s.initMeta()
	}
	if err != nil {
		return fmt.Errorf("read utxo meta: %w", err)
	}
	defer closer.Close()
	var m utxoMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		s.invalidReason = "unreadable metadata"
		return nil
	}
	if m.Schema != utxoSchemaVersion || m.PebbleFormat != int(utxoPebbleFormat) {
		s.invalidReason = fmt.Sprintf("format version %d/%d, expected %d/%d",
			m.Schema, m.PebbleFormat, utxoSchemaVersion, int(utxoPebbleFormat))
		return nil
	}
	if m.Ingesting {
		s.discard = true
		return nil
	}
	if _, err := decodeUtxoTip(m); err != nil {
		s.invalidReason = "unreadable tip"
		return nil
	}
	s.meta = m
	return nil
}

func (s *utxoStore) initMeta() error {
	it, err := s.db.NewIter(nil)
	if err != nil {
		return err
	}
	populated := it.First()
	if err := it.Close(); err != nil {
		return err
	}
	if populated {
		s.invalidReason = "missing metadata"
		return nil
	}
	s.meta = utxoMeta{Schema: utxoSchemaVersion, PebbleFormat: int(utxoPebbleFormat)}
	return s.db.Set(utxoMetaKey, mustMarshalUtxoMeta(s.meta), pebble.Sync)
}

func mustMarshalUtxoMeta(m utxoMeta) []byte {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

func decodeUtxoTip(m utxoMeta) ([32]byte, error) {
	var h [32]byte
	if !m.HasTip {
		return h, nil
	}
	b, err := hex.DecodeString(m.TipHash)
	if err != nil || len(b) != len(h) {
		return h, errors.New("bad tip hash")
	}
	copy(h[:], b)
	return h, nil
}

func (s *utxoStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *utxoStore) Valid() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.invalidReason == ""
}

func (s *utxoStore) InvalidReason() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.invalidReason
}

func (s *utxoStore) usable() error {
	if err := s.brokenError(); err != nil {
		return err
	}
	if s.invalidReason != "" {
		return fmt.Errorf("%w: %s", errUtxoInvalid, s.invalidReason)
	}
	if s.db == nil {
		return errUtxoClosed
	}
	return nil
}

func (s *utxoStore) Tip() (height uint32, hash [32]byte, ok bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err = s.usable(); err != nil {
		return
	}
	hash, _ = decodeUtxoTip(s.meta)
	return s.meta.TipHeight, hash, s.meta.HasTip, nil
}

func (s *utxoStore) ApplyBlock(blk utxoBlock) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return err
	}
	if s.meta.HasTip {
		tip, _ := decodeUtxoTip(s.meta)
		if blk.Height != s.meta.TipHeight+1 || blk.PrevHash != tip {
			return errUtxoNotContiguous
		}
	}
	b := s.db.NewIndexedBatch()
	defer b.Close()
	var inverse []byte
	for _, c := range blk.Creates {
		if !utxoSpendable(c.Script) {
			continue
		}
		key := utxoCoinKey(utxoScriptHash(c.Script), c.utxoOutpoint)
		old, closer, err := b.Get(key)
		if err == nil {
			inverse = appendUtxoSet(inverse, key, old)
			closer.Close()
		} else if errors.Is(err, pebble.ErrNotFound) {
			inverse = appendUtxoDelete(inverse, key)
		} else {
			return err
		}
		if err := b.Set(key, utxoCoinValue(c.Amount, blk.Height), nil); err != nil {
			return err
		}
	}
	for _, sp := range blk.Spends {
		key := utxoCoinKey(utxoScriptHash(sp.Script), sp.utxoOutpoint)
		val, closer, err := b.Get(key)
		if errors.Is(err, pebble.ErrNotFound) {
			return errUtxoMissingCoin
		}
		if err != nil {
			return err
		}
		inverse = appendUtxoSet(inverse, key, val)
		closer.Close()
		if err := b.Delete(key, nil); err != nil {
			return err
		}
	}
	undo := make([]byte, 0, 32+len(inverse))
	undo = append(undo, blk.Hash[:]...)
	undo = append(undo, blk.PrevHash[:]...)
	undo = append(undo, reverseUtxoOps(inverse)...)
	if err := b.Set(utxoUndoKey(blk.Height), undo, nil); err != nil {
		return err
	}
	if blk.Height > utxoJournalDepth {
		if err := b.Delete(utxoUndoKey(blk.Height-utxoJournalDepth), nil); err != nil {
			return err
		}
	}
	meta := utxoMeta{Schema: utxoSchemaVersion, PebbleFormat: int(utxoPebbleFormat),
		HasTip: true, TipHeight: blk.Height, TipHash: hex.EncodeToString(blk.Hash[:])}
	if err := b.Set(utxoMetaKey, mustMarshalUtxoMeta(meta), nil); err != nil {
		return err
	}
	if s.beforeCommit != nil {
		if err := s.beforeCommit(); err != nil {
			return err
		}
	}
	if err := s.commit(b); err != nil {
		return err
	}
	s.meta = meta
	return nil
}

// An undo op is kind (1 set, 0 delete) || key || value (set only).
func appendUtxoSet(ops, key, val []byte) []byte {
	ops = append(ops, 1)
	ops = append(ops, key...)
	return append(ops, val...)
}

func appendUtxoDelete(ops, key []byte) []byte {
	ops = append(ops, 0)
	return append(ops, key...)
}

func splitUtxoOps(ops []byte) ([][]byte, error) {
	var out [][]byte
	for len(ops) > 0 {
		n := 1 + utxoKeyLen
		if ops[0] == 1 {
			n += utxoValueLen
		} else if ops[0] != 0 {
			return nil, errors.New("corrupt undo record")
		}
		if len(ops) < n {
			return nil, errors.New("truncated undo record")
		}
		out = append(out, ops[:n])
		ops = ops[n:]
	}
	return out, nil
}

func reverseUtxoOps(ops []byte) []byte {
	parts, err := splitUtxoOps(ops)
	if err != nil {
		panic(err)
	}
	out := make([]byte, 0, len(ops))
	for i := len(parts) - 1; i >= 0; i-- {
		out = append(out, parts[i]...)
	}
	return out
}

func (s *utxoStore) DisconnectTip() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return err
	}
	if !s.meta.HasTip {
		return errUtxoNoTip
	}
	height := s.meta.TipHeight
	undo, closer, err := s.db.Get(utxoUndoKey(height))
	if errors.Is(err, pebble.ErrNotFound) {
		return errUtxoNoUndo
	}
	if err != nil {
		return err
	}
	raw := bytes.Clone(undo)
	closer.Close()
	if len(raw) < 64 {
		return errors.New("corrupt undo record")
	}
	ops, err := splitUtxoOps(raw[64:])
	if err != nil {
		return err
	}
	b := s.db.NewBatch()
	defer b.Close()
	for _, op := range ops {
		key := op[1 : 1+utxoKeyLen]
		if op[0] == 1 {
			err = b.Set(key, op[1+utxoKeyLen:], nil)
		} else {
			err = b.Delete(key, nil)
		}
		if err != nil {
			return err
		}
	}
	if err := b.Delete(utxoUndoKey(height), nil); err != nil {
		return err
	}
	meta := utxoMeta{Schema: utxoSchemaVersion, PebbleFormat: int(utxoPebbleFormat)}
	if height > 0 {
		meta.HasTip, meta.TipHeight, meta.TipHash = true, height-1, hex.EncodeToString(raw[32:64])
	}
	if err := b.Set(utxoMetaKey, mustMarshalUtxoMeta(meta), nil); err != nil {
		return err
	}
	if s.beforeCommit != nil {
		if err := s.beforeCommit(); err != nil {
			return err
		}
	}
	if err := s.commit(b); err != nil {
		return err
	}
	s.meta = meta
	return nil
}

func (s *utxoStore) Lookup(scripts [][]byte) ([]utxoCoin, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.usable(); err != nil {
		return nil, err
	}
	seen := make(map[[utxoPrefixLen]byte]bool, len(scripts))
	var out []utxoCoin
	for _, script := range scripts {
		h := utxoScriptHash(script)
		if seen[h] {
			continue
		}
		seen[h] = true
		coins, err := s.scanPrefix(h)
		if err != nil {
			return nil, err
		}
		out = append(out, coins...)
	}
	return out, nil
}

func (s *utxoStore) scanPrefix(h [utxoPrefixLen]byte) ([]utxoCoin, error) {
	upper := bytes.Clone(h[:])
	for i := len(upper) - 1; i >= 0; i-- {
		upper[i]++
		if upper[i] != 0 {
			break
		}
		if i == 0 {
			upper = nil
		}
	}
	it, err := s.db.NewIter(&pebble.IterOptions{LowerBound: h[:], UpperBound: upper})
	if err != nil {
		return nil, err
	}
	var out []utxoCoin
	for ok := it.First(); ok; ok = it.Next() {
		k, v := it.Key(), it.Value()
		if len(k) != utxoKeyLen || len(v) != utxoValueLen {
			_ = it.Close()
			return nil, errors.New("corrupt coin record")
		}
		c := utxoCoin{ScriptHash: h, Amount: binary.BigEndian.Uint64(v), Height: binary.BigEndian.Uint32(v[8:])}
		copy(c.Txid[:], k[utxoPrefixLen:])
		c.Vout = binary.BigEndian.Uint32(k[utxoPrefixLen+32:])
		out = append(out, c)
	}
	return out, it.Close()
}

func (s *utxoStore) emptyLocked() error {
	if s.meta.HasTip {
		return errUtxoNotEmpty
	}
	it, err := s.db.NewIter(nil)
	if err != nil {
		return err
	}
	for ok := it.First(); ok; ok = it.Next() {
		if !bytes.Equal(it.Key(), utxoMetaKey) {
			_ = it.Close()
			return errUtxoNotEmpty
		}
	}
	return it.Close()
}

// The index directory can be a volume mount point, which cannot itself be removed.
func clearUtxoDir(fs vfs.FS, dir string) error {
	names, err := fs.List(dir)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := fs.RemoveAll(fs.PathJoin(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

func (s *utxoStore) RequireEmpty() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.usable(); err != nil {
		return err
	}
	return s.emptyLocked()
}

func (s *utxoStore) setMeta(m utxoMeta) error {
	b := s.db.NewBatch()
	defer b.Close()
	if err := b.Set(utxoMetaKey, mustMarshalUtxoMeta(m), nil); err != nil {
		return err
	}
	return s.commit(b)
}

// IngestFiles loads sorted, non-overlapping SSTs into an empty store; until it returns nil the store refuses reads and a reopen discards it.
func (s *utxoStore) IngestFiles(paths []string, height uint32, hash [32]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return err
	}
	if err := s.emptyLocked(); err != nil {
		return err
	}
	pending := s.meta
	pending.Ingesting = true
	if err := s.setMeta(pending); err != nil {
		return err
	}
	s.invalidReason = "ingest in progress"
	if len(paths) > 0 {
		if err := s.db.Ingest(paths); err != nil {
			return err
		}
	}
	if s.beforeIngest != nil {
		if err := s.beforeIngest(); err != nil {
			return err
		}
	}
	meta := utxoMeta{Schema: utxoSchemaVersion, PebbleFormat: int(utxoPebbleFormat),
		HasTip: true, TipHeight: height, TipHash: hex.EncodeToString(hash[:])}
	if err := s.setMeta(meta); err != nil {
		return err
	}
	s.meta = meta
	s.invalidReason = ""
	return nil
}
