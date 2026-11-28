package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
)

const (
	utxoSchemaVersion   = 1
	utxoSchemaReverse   = 2
	utxoSchemaCanonical = 2
	utxoPebbleFormat    = pebble.FormatFlushableIngest
	utxoJournalDepth    = 144
	utxoPrefixLen       = 16
	utxoKeyLen          = utxoPrefixLen + 32 + 4
	utxoValueLen        = 12
	utxoRevTag          = 'o'
	utxoRevKeyLen       = 1 + 32 + 4
	utxoPiecesTag       = 2
	utxoMaxScriptLen    = 10000
	utxoBlockCacheBytes = 64 << 20
	utxoMemTableBytes   = 16 << 20
	utxoCompactions     = 1
	utxoIngestTmpDir    = "ingest-tmp"
)

const utxoChainstateLogDir = "chainstate-log"

// A block with more coin operations than this commits in pieces of about 9 MB of batch each, so a 32 MB Bitcoin Cash block fits the 1 GiB cap.
var utxoBlockPieceOps = 50_000

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
	errUtxoChainMismatch = errors.New("utxo index chain mismatch")
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
	P2PK   bool
}

type utxoMeta struct {
	Schema       int    `json:"schema"`
	PebbleFormat int    `json:"pebble_format"`
	HasTip       bool   `json:"has_tip"`
	TipHeight    uint32 `json:"tip_height"`
	TipHash      string `json:"tip_hash"`
	Ingesting    bool   `json:"ingesting,omitempty"`
	ChainName    string `json:"chain_name,omitempty"`
	GenesisHash  string `json:"genesis_hash,omitempty"`
	BaseHeight   uint32 `json:"base_height,omitempty"`
	BaseHash     string `json:"base_hash,omitempty"`
	Invalid      string `json:"invalid,omitempty"`
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
	reverse       bool
	canon         bool
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

func utxoCoinValue(amount uint64, height uint32, p2pk bool) []byte {
	if p2pk {
		height |= utxoP2PKBit
	}
	v := binary.BigEndian.AppendUint64(make([]byte, 0, utxoValueLen), amount)
	return binary.BigEndian.AppendUint32(v, height)
}

// keyHash is the index prefix of a script; a canonical store files a P2PK script under its key's P2PKH address.
func (s *utxoStore) keyHash(script []byte) (h [utxoPrefixLen]byte, p2pk bool) {
	if s.canon {
		return utxoCanonHash(script)
	}
	return utxoScriptHash(script), false
}

// Legacy reports an index built before P2PK coins were filed under their address.
func (s *utxoStore) Legacy() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.invalidReason == "" && s.meta.HasTip && !s.canon
}

// A reverse key maps an outpoint to its script hash, for chains whose blocks carry no prevout scripts.
func utxoRevKey(coinKey []byte) []byte {
	return append([]byte{utxoRevTag}, coinKey[utxoPrefixLen:]...)
}

// Undo keys are 8 bytes and the meta key 4, so neither can fall inside a 16-byte coin prefix range.
func utxoUndoKey(height uint32) []byte {
	return binary.BigEndian.AppendUint32([]byte("undo"), height)
}

// A piece key extends its block's undo key, so the pieces of a block sort right after its record.
func utxoPieceKey(height, piece uint32) []byte {
	return binary.BigEndian.AppendUint32(utxoUndoKey(height), piece)
}

func openUtxoStore(dir string) (*utxoStore, error) {
	return openUtxoStoreFS(dir, nil, false)
}

func openUtxoStoreFS(dir string, fs vfs.FS, reverse bool) (*utxoStore, error) {
	if fs == nil {
		fs = vfs.Default
	}
	s := &utxoStore{dir: dir, fs: fs, reverse: reverse}
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
		return openUtxoStoreFS(dir, fs, reverse)
	}
	if s.invalidReason != "" {
		_ = db.Close()
		s.db = nil
		return s, nil
	}
	if err := s.rollBackPartialBlock(); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("roll back a partly applied block: %w", err)
	}
	for _, tmp := range []string{utxoIngestTmpDir, utxoChainstateLogDir} {
		if err := fs.RemoveAll(fs.PathJoin(dir, tmp)); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("clear utxo ingest temp: %w", err)
		}
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
	if m.Invalid != "" {
		s.invalidReason = m.Invalid
		return nil
	}
	base := s.baseSchema()
	if m.Schema != base && m.Schema != base+utxoSchemaCanonical || m.PebbleFormat != int(utxoPebbleFormat) {
		s.invalidReason = fmt.Sprintf("format version %d/%d, expected %d/%d",
			m.Schema, m.PebbleFormat, base+utxoSchemaCanonical, int(utxoPebbleFormat))
		return nil
	}
	s.canon = m.Schema != base
	if !s.canon && !m.HasTip && !m.Ingesting {
		if empty, err := s.hasNoCoins(); err != nil {
			return err
		} else if empty {
			s.canon, m.Schema = true, base+utxoSchemaCanonical
			if err := s.setMeta(m); err != nil {
				return err
			}
		}
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
	s.canon = true
	s.meta = utxoMeta{Schema: s.schema(), PebbleFormat: int(utxoPebbleFormat)}
	return s.db.Set(utxoMetaKey, mustMarshalUtxoMeta(s.meta), pebble.Sync)
}

func (s *utxoStore) baseSchema() int {
	if s.reverse {
		return utxoSchemaReverse
	}
	return utxoSchemaVersion
}

func (s *utxoStore) schema() int {
	if s.canon {
		return s.baseSchema() + utxoSchemaCanonical
	}
	return s.baseSchema()
}

func (s *utxoStore) hasNoCoins() (bool, error) {
	it, err := s.db.NewIter(nil)
	if err != nil {
		return false, err
	}
	empty := true
	for ok := it.First(); ok && empty; ok = it.Next() {
		empty = bytes.Equal(it.Key(), utxoMetaKey)
	}
	return empty, it.Close()
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
	return s.ApplyBlocks([]utxoBlock{blk})
}

// ApplyBlocks writes contiguous blocks in one batch with one sync and one meta write; a block over utxoBlockPieceOps commits on its own, in pieces.
func (s *utxoStore) ApplyBlocks(blks []utxoBlock) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return err
	}
	start := 0
	for i, blk := range blks {
		if len(blk.Creates)+len(blk.Spends) <= utxoBlockPieceOps {
			continue
		}
		if err := s.applyBatchLocked(blks[start:i]); err != nil {
			return err
		}
		if err := s.applyPiecesLocked(blk); err != nil {
			return err
		}
		start = i + 1
	}
	return s.applyBatchLocked(blks[start:])
}

func extendsUtxoTip(m utxoMeta, blk utxoBlock) bool {
	tip, _ := decodeUtxoTip(m)
	return !m.HasTip || blk.Height == m.TipHeight+1 && blk.PrevHash == tip
}

func withUtxoTip(m utxoMeta, blk utxoBlock) utxoMeta {
	m.HasTip, m.TipHeight, m.TipHash = true, blk.Height, hex.EncodeToString(blk.Hash[:])
	return m
}

func (s *utxoStore) applyBatchLocked(blks []utxoBlock) error {
	if len(blks) == 0 {
		return nil
	}
	b := s.db.NewIndexedBatch()
	defer b.Close()
	meta := s.meta
	for _, blk := range blks {
		if !extendsUtxoTip(meta, blk) {
			return errUtxoNotContiguous
		}
		inverse, err := s.writeCoinsLocked(b, blk.Height, blk.Creates, blk.Spends)
		if err != nil {
			return err
		}
		if err := writeUtxoUndoHead(b, blk, 0, inverse); err != nil {
			return err
		}
		meta = withUtxoTip(meta, blk)
	}
	return s.commitLocked(b, &meta)
}

// applyPiecesLocked commits a large block in pieces so no batch or undo record holds the whole block; only the last piece moves the tip.
func (s *utxoStore) applyPiecesLocked(blk utxoBlock) error {
	if !extendsUtxoTip(s.meta, blk) {
		return errUtxoNotContiguous
	}
	creates, spends := blk.Creates, blk.Spends
	for piece := uint32(0); ; piece++ {
		nc := min(len(creates), utxoBlockPieceOps)
		ns := min(len(spends), utxoBlockPieceOps-nc)
		last := nc == len(creates) && ns == len(spends)
		if err := s.applyPieceLocked(blk, piece, creates[:nc], spends[:ns], last); err != nil {
			return errors.Join(err, s.rollBackPiecesLocked(blk.Height, piece))
		}
		if last {
			return nil
		}
		creates, spends = creates[nc:], spends[ns:]
	}
}

func (s *utxoStore) applyPieceLocked(blk utxoBlock, piece uint32, creates []utxoCreate, spends []utxoSpend, last bool) error {
	b := s.db.NewIndexedBatch()
	defer b.Close()
	inverse, err := s.writeCoinsLocked(b, blk.Height, creates, spends)
	if err != nil {
		return err
	}
	if !last {
		if err := b.Set(utxoPieceKey(blk.Height, piece), appendReversedUtxoOps(nil, inverse), nil); err != nil {
			return err
		}
		return s.commitLocked(b, nil)
	}
	if err := writeUtxoUndoHead(b, blk, piece, inverse); err != nil {
		return err
	}
	meta := withUtxoTip(s.meta, blk)
	return s.commitLocked(b, &meta)
}

func (s *utxoStore) commitLocked(b *pebble.Batch, meta *utxoMeta) error {
	if meta != nil {
		if err := b.Set(utxoMetaKey, mustMarshalUtxoMeta(*meta), nil); err != nil {
			return err
		}
	}
	if s.beforeCommit != nil {
		if err := s.beforeCommit(); err != nil {
			return err
		}
	}
	if err := s.commit(b); err != nil {
		return err
	}
	if meta != nil {
		s.meta = *meta
	}
	return nil
}

// writeCoinsLocked applies creates before spends, since a block's transactions can spend outputs of later ones, and returns the inverse ops.
func (s *utxoStore) writeCoinsLocked(b *pebble.Batch, height uint32, creates []utxoCreate, spends []utxoSpend) ([]byte, error) {
	inverse := make([]byte, 0, (len(creates)+len(spends))*(1+utxoKeyLen+utxoValueLen))
	keys, flags, order := make([][]byte, len(creates)), make([]bool, len(creates)), make([]int, 0, len(creates))
	if height >= utxoP2PKBit {
		return nil, fmt.Errorf("block height %d is out of range", height)
	}
	for i, c := range creates {
		if utxoSpendable(c.Script) {
			h, p2pk := s.keyHash(c.Script)
			keys[i], flags[i], order = utxoCoinKey(h, c.utxoOutpoint), p2pk, append(order, i)
		}
	}
	// Reading in key order lets neighbouring lookups share cached blocks once a large block's earlier pieces are on disk.
	slices.SortStableFunc(order, func(a, b int) int { return bytes.Compare(keys[a], keys[b]) })
	for _, i := range order {
		c, key := creates[i], keys[i]
		old, closer, err := b.Get(key)
		if err == nil {
			inverse = appendUtxoSet(inverse, key, old)
			closer.Close()
		} else if errors.Is(err, pebble.ErrNotFound) {
			inverse = appendUtxoDelete(inverse, key)
		} else {
			return nil, err
		}
		if err := b.Set(key, utxoCoinValue(c.Amount, height, flags[i]), nil); err != nil {
			return nil, err
		}
		if s.reverse {
			if err := b.Set(utxoRevKey(key), key[:utxoPrefixLen], nil); err != nil {
				return nil, err
			}
		}
	}
	for _, sp := range spends {
		h, _ := s.keyHash(sp.Script)
		key := utxoCoinKey(h, sp.utxoOutpoint)
		if s.reverse {
			if err := takeUtxoRev(b, key); err != nil {
				return nil, err
			}
		}
		val, closer, err := b.Get(key)
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, errUtxoMissingCoin
		}
		if err != nil {
			return nil, err
		}
		inverse = appendUtxoSet(inverse, key, val)
		closer.Close()
		if err := b.Delete(key, nil); err != nil {
			return nil, err
		}
	}
	return inverse, nil
}

// writeUtxoUndoHead writes the block's undo record; a block applied in pieces marks how many earlier pieces its record leaves out.
func writeUtxoUndoHead(b *pebble.Batch, blk utxoBlock, pieces uint32, inverse []byte) error {
	undo := make([]byte, 0, 64+5+len(inverse))
	undo = append(undo, blk.Hash[:]...)
	undo = append(undo, blk.PrevHash[:]...)
	if pieces > 0 {
		undo = binary.BigEndian.AppendUint32(append(undo, utxoPiecesTag), pieces)
	}
	undo = appendReversedUtxoOps(undo, inverse)
	if err := b.Set(utxoUndoKey(blk.Height), undo, nil); err != nil {
		return err
	}
	if blk.Height <= utxoJournalDepth {
		return nil
	}
	return deleteUtxoUndo(b, blk.Height-utxoJournalDepth)
}

func deleteUtxoUndo(b *pebble.Batch, height uint32) error {
	var pieces uint32
	head, closer, err := b.Get(utxoUndoKey(height))
	if err == nil {
		_, pieces, err = splitUtxoUndoHead(head)
		closer.Close()
	} else if errors.Is(err, pebble.ErrNotFound) {
		err = nil
	}
	if err != nil {
		return err
	}
	for p := range pieces {
		if err := b.Delete(utxoPieceKey(height, p), nil); err != nil {
			return err
		}
	}
	return b.Delete(utxoUndoKey(height), nil)
}

// splitUtxoUndoHead returns the head record's own ops and how many earlier pieces the block has.
func splitUtxoUndoHead(undo []byte) (ops []byte, pieces uint32, err error) {
	if len(undo) < 64 {
		return nil, 0, errors.New("corrupt undo record")
	}
	ops = undo[64:]
	if len(ops) > 0 && ops[0] == utxoPiecesTag {
		if len(ops) < 5 {
			return nil, 0, errors.New("corrupt undo record")
		}
		pieces, ops = binary.BigEndian.Uint32(ops[1:5]), ops[5:]
	}
	return ops, pieces, nil
}

// takeUtxoRev deletes the spent outpoint's reverse key and writes its script hash into key.
func takeUtxoRev(b *pebble.Batch, key []byte) error {
	rk := utxoRevKey(key)
	h, closer, err := b.Get(rk)
	if errors.Is(err, pebble.ErrNotFound) {
		return errUtxoMissingCoin
	}
	if err != nil {
		return err
	}
	ok := len(h) == utxoPrefixLen
	copy(key, h)
	closer.Close()
	if !ok {
		return errors.New("corrupt reverse record")
	}
	return b.Delete(rk, nil)
}

// An undo op is kind (1 set, 0 delete) || key || value (set only); reverse keys are derived from it, not journaled.
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

// appendReversedUtxoOps appends well-formed ops last first without copying them apart, since a large block's undo runs to tens of megabytes.
func appendReversedUtxoOps(dst, ops []byte) []byte {
	starts := make([]int, 0, len(ops)/(1+utxoKeyLen))
	for i := 0; i < len(ops); {
		starts = append(starts, i)
		i += 1 + utxoKeyLen
		if ops[starts[len(starts)-1]] == 1 {
			i += utxoValueLen
		}
	}
	end := len(ops)
	for k := len(starts) - 1; k >= 0; k-- {
		dst = append(dst, ops[starts[k]:end]...)
		end = starts[k]
	}
	return dst
}

func (s *utxoStore) UndoPrevHash(height uint32) (prev [32]byte, ok bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.usable(); err != nil {
		return prev, false, err
	}
	undo, closer, err := s.db.Get(utxoUndoKey(height))
	if errors.Is(err, pebble.ErrNotFound) {
		return prev, false, nil
	}
	if err != nil {
		return prev, false, err
	}
	defer closer.Close()
	if len(undo) < 64 {
		return prev, false, errors.New("corrupt undo record")
	}
	copy(prev[:], undo[32:64])
	return prev, true, nil
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
	ops, pieces, err := splitUtxoUndoHead(raw)
	if err != nil {
		return err
	}
	b := s.db.NewBatch()
	defer b.Close()
	if err := s.writeUndoOpsLocked(b, ops); err != nil {
		return err
	}
	if err := b.Delete(utxoUndoKey(height), nil); err != nil {
		return err
	}
	meta := s.meta
	meta.HasTip, meta.TipHeight, meta.TipHash = false, 0, ""
	if height > 0 {
		meta.HasTip, meta.TipHeight, meta.TipHash = true, height-1, hex.EncodeToString(raw[32:64])
	}
	if err := s.commitLocked(b, &meta); err != nil {
		return err
	}
	return s.rollBackPiecesLocked(height, pieces)
}

func (s *utxoStore) writeUndoOpsLocked(b *pebble.Batch, raw []byte) error {
	ops, err := splitUtxoOps(raw)
	if err != nil {
		return err
	}
	for _, op := range ops {
		key := op[1 : 1+utxoKeyLen]
		if op[0] == 1 {
			err = b.Set(key, op[1+utxoKeyLen:], nil)
		} else {
			err = b.Delete(key, nil)
		}
		if err == nil && s.reverse {
			if op[0] == 1 {
				err = b.Set(utxoRevKey(key), key[:utxoPrefixLen], nil)
			} else {
				err = b.Delete(utxoRevKey(key), nil)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// rollBackPartialBlock undoes the pieces a crash left above the tip, before anything reads the store.
func (s *utxoStore) rollBackPartialBlock() error {
	// A store with no tip takes its first block at any height.
	bounds := &pebble.IterOptions{LowerBound: utxoUndoKey(0), UpperBound: []byte("undp")}
	if s.meta.HasTip {
		bounds = &pebble.IterOptions{LowerBound: utxoPieceKey(s.meta.TipHeight+1, 0), UpperBound: utxoUndoKey(s.meta.TipHeight + 2)}
	}
	it, err := s.db.NewIter(bounds)
	if err != nil {
		return err
	}
	var height, pieces uint32
	for ok := it.First(); ok; ok = it.Next() {
		if k := it.Key(); len(k) == len(utxoPieceKey(0, 0)) {
			height, pieces = binary.BigEndian.Uint32(k[4:8]), pieces+1
		}
	}
	if err := it.Close(); err != nil {
		return err
	}
	return s.rollBackPiecesLocked(height, pieces)
}

// rollBackPiecesLocked undoes a block's first n pieces, last first, one batch each; if that fails the store stops serving, and a reopen finishes it.
func (s *utxoStore) rollBackPiecesLocked(height, n uint32) error {
	if n == 0 || s.usable() != nil {
		return nil
	}
	for n > 0 {
		n--
		if err := s.rollBackPieceLocked(height, n); err != nil {
			msg := fmt.Sprintf("block %d is partly applied: %v", height, err)
			s.fatal.CompareAndSwap(nil, &msg)
			s.abandon()
			return s.brokenError()
		}
	}
	return nil
}

func (s *utxoStore) rollBackPieceLocked(height, piece uint32) error {
	key := utxoPieceKey(height, piece)
	rec, closer, err := s.db.Get(key)
	if err != nil {
		return err
	}
	ops := bytes.Clone(rec)
	closer.Close()
	b := s.db.NewBatch()
	defer b.Close()
	if err := s.writeUndoOpsLocked(b, ops); err != nil {
		return err
	}
	if err := b.Delete(key, nil); err != nil {
		return err
	}
	return s.commitLocked(b, nil)
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
		h, _ := s.keyHash(script)
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
		raw := binary.BigEndian.Uint32(v[8:])
		c := utxoCoin{ScriptHash: h, Amount: binary.BigEndian.Uint64(v), Height: raw &^ utxoP2PKBit, P2PK: raw&utxoP2PKBit != 0}
		copy(c.Txid[:], k[utxoPrefixLen:])
		c.Vout = binary.BigEndian.Uint32(k[utxoPrefixLen+32:])
		out = append(out, c)
	}
	return out, it.Close()
}

// Totals counts every coin and sums its amount at the tip; the caller keeps the follower out so the tip cannot move.
func (s *utxoStore) Totals(ctx context.Context) (coins, sats uint64, err error) {
	t, err := s.TotalsP2PK(ctx)
	return t.Coins, t.Sats, err
}

type utxoTotals struct{ Coins, Sats, P2PKCoins, P2PKSats uint64 }

func (s *utxoStore) TotalsP2PK(ctx context.Context) (utxoTotals, error) {
	var t utxoTotals
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.usable(); err != nil {
		return t, err
	}
	it, err := s.db.NewIter(nil)
	if err != nil {
		return t, err
	}
	defer it.Close()
	for ok := it.First(); ok; ok = it.Next() {
		if len(it.Key()) != utxoKeyLen {
			continue
		}
		v := it.Value()
		if len(v) != utxoValueLen {
			return t, errors.New("corrupt coin record")
		}
		amount := binary.BigEndian.Uint64(v)
		// shortcut: uint64 holds 184.4 billion coins of 10^8 units, which Dogecoin reaches around 2032; sum in big.Int before then.
		var carry uint64
		if t.Sats, carry = bits.Add64(t.Sats, amount, 0); carry != 0 {
			return t, errors.New("the coin total overflows 64 bits")
		}
		if binary.BigEndian.Uint32(v[8:])&utxoP2PKBit != 0 {
			t.P2PKCoins++
			t.P2PKSats += amount
		}
		if t.Coins++; t.Coins%(1<<16) == 0 && ctx.Err() != nil {
			return t, ctx.Err()
		}
	}
	return t, it.Error()
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
	meta := s.meta
	meta.HasTip, meta.TipHeight, meta.TipHash = true, height, hex.EncodeToString(hash[:])
	meta.BaseHeight, meta.BaseHash = height, meta.TipHash
	if err := s.setMeta(meta); err != nil {
		return err
	}
	s.meta = meta
	s.invalidReason = ""
	return nil
}

func (s *utxoStore) BindChain(name, genesis string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return err
	}
	if s.meta.ChainName == name && s.meta.GenesisHash == genesis {
		return nil
	}
	if s.meta.ChainName != "" || s.meta.GenesisHash != "" {
		return fmt.Errorf("%w: index was built for a different chain", errUtxoChainMismatch)
	}
	meta := s.meta
	meta.ChainName, meta.GenesisHash = name, genesis
	if err := s.setMeta(meta); err != nil {
		return err
	}
	s.meta = meta
	return nil
}

// The in-memory flag is set even when persisting fails, so the index stops serving either way.
func (s *utxoStore) MarkInvalid(reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.invalidReason != "" {
		return nil
	}
	s.invalidReason = reason
	var err error
	if s.db == nil || s.brokenError() != nil {
		err = errUtxoBroken
	} else {
		meta := s.meta
		meta.Invalid = reason
		err = s.setMeta(meta)
	}
	s.abandon()
	return err
}
