package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sync/atomic"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
)

var errChainstateFormat = errors.New("unexpected chainstate format")

// The key LevelDB-backed Bitcoin Core forks store their 8-byte XOR key under: a serialized string, length byte first.
var chainstateObfuscateKey = append([]byte{14, 0}, "obfuscate_key"...)

// The copy sits on the dashboard's read-only mount of the node's data, so it is opened without taking a lock.
type chainstateFS struct{ vfs.FS }

func (chainstateFS) Lock(string) (io.Closer, error) { return io.NopCloser(nil), nil }

type chainstateLogger struct{ fatal *atomic.Pointer[string] }

func (chainstateLogger) Infof(string, ...interface{}) {}

func (l chainstateLogger) Fatalf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	l.fatal.CompareAndSwap(nil, &msg)
}

// chainstateCopy reads a stopped node's chainstate, copied by the host helper with its LevelDB tables renamed to .sst for Pebble.
type chainstateCopy struct {
	db   *pebble.DB
	xor  []byte
	Best [32]byte
	// txout selects the per-output rows of Bitcoin Core 0.15 and its descendants over the per-transaction rows of Core 0.14 and Dogecoin 1.14.
	txout bool
	fatal atomic.Pointer[string]
}

func openChainstateCopy(dir string) (*chainstateCopy, error) {
	// The node's user owns the copy, so a symlink in it could point the reader at files the node cannot read itself.
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("open chainstate copy: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a directory", errChainstateFormat, dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("open chainstate copy: %w", err)
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			return nil, fmt.Errorf("%w: %s is not a regular file", errChainstateFormat, e.Name())
		}
	}
	c := &chainstateCopy{}
	db, err := pebble.Open(dir, &pebble.Options{ReadOnly: true, FS: chainstateFS{vfs.Default}, Logger: chainstateLogger{&c.fatal}})
	if err != nil {
		return nil, fmt.Errorf("open chainstate copy: %w", err)
	}
	c.db = db
	// Any early return, a panic included, closes the copy so no descriptor pins its tables.
	opened := false
	defer func() {
		if !opened {
			_ = db.Close()
		}
	}()
	key, err := c.get(chainstateObfuscateKey)
	switch {
	case errors.Is(err, pebble.ErrNotFound):
	case err != nil:
		return nil, err
	case len(key) != 9 || key[0] != 8:
		return nil, fmt.Errorf("%w: obfuscation key %x", errChainstateFormat, key)
	default:
		c.xor = key[1:]
	}
	// A node killed mid-flush leaves its best block behind its tables, which this key marks.
	if _, err := c.get([]byte{'H'}); err == nil {
		return nil, fmt.Errorf("%w: the copy was taken while the node was flushing", errChainstateFormat)
	} else if !errors.Is(err, pebble.ErrNotFound) {
		return nil, err
	}
	best, err := c.get([]byte{'B'})
	if err != nil || len(best) != 32 {
		return nil, fmt.Errorf("%w: no best block (%v)", errChainstateFormat, err)
	}
	c.unmask(best)
	copy(c.Best[:], best)
	opened = true
	return c, nil
}

func (c *chainstateCopy) Close() error { return c.db.Close() }

func (c *chainstateCopy) get(key []byte) ([]byte, error) {
	v, closer, err := c.db.Get(key)
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	return bytes.Clone(v), nil
}

func (c *chainstateCopy) unmask(v []byte) {
	if len(c.xor) == 0 {
		return
	}
	for i := range v {
		v[i] ^= c.xor[i%len(c.xor)]
	}
}

// rows calls fn for each coins row, unmasked; the slice is reused between calls.
// A 1.14 row holds one transaction's outputs and has vout 0 here; a per-output row names its output in its key.
func (c *chainstateCopy) rows(fn func(txid [32]byte, vout uint32, row []byte) error) error {
	prefix := byte('c')
	if c.txout {
		prefix = 'C'
	}
	it, err := c.db.NewIter(&pebble.IterOptions{LowerBound: []byte{prefix}, UpperBound: []byte{prefix + 1}})
	if err != nil {
		return err
	}
	var row []byte
	for it.First(); it.Valid(); it.Next() {
		txid, vout, ok := c.splitKey(it.Key())
		if !ok {
			err := fmt.Errorf("%w: coins key of %d bytes", errChainstateFormat, len(it.Key()))
			_ = it.Close()
			return err
		}
		row = append(row[:0], it.Value()...)
		c.unmask(row)
		if err := fn(txid, vout, row); err != nil {
			_ = it.Close()
			return err
		}
	}
	if err := it.Close(); err != nil {
		return err
	}
	if msg := c.fatal.Load(); msg != nil {
		return fmt.Errorf("chainstate copy: %s", *msg)
	}
	return nil
}

// splitKey reads a 1.14 key, the prefix and a txid, or a per-output key, which adds the output index as a base-128 varint of one to five bytes.
func (c *chainstateCopy) splitKey(key []byte) (txid [32]byte, vout uint32, ok bool) {
	if len(key) < 33 || !c.txout && len(key) != 33 || len(key) > 38 {
		return txid, 0, false
	}
	copy(txid[:], key[1:33])
	if !c.txout {
		return txid, 0, true
	}
	var n uint64
	for i, b := range key[33:] {
		n = n<<7 | uint64(b&0x7f)
		if b&0x80 == 0 {
			return txid, uint32(n), i == len(key)-34 && n <= math.MaxUint32
		}
		n++
	}
	return txid, 0, false
}

// parse counts the coins first, so the ingest can size its runs and report progress, then streams them.
func (c *chainstateCopy) parse(onHeader func(utxoHeader), fn func(utxoRecord) error) (utxoHeader, error) {
	u := &utxoReader{r: bufio.NewReaderSize(nil, 4096), oversizeUnspendable: true}
	var br bytes.Reader
	h := utxoHeader{BaseHash: c.Best}
	err := c.rows(func(txid [32]byte, _ uint32, row []byte) error {
		if c.txout {
			h.CoinsCount++
			return nil
		}
		br.Reset(row)
		u.r.Reset(&br)
		vouts, _, err := u.coinsHeader(len(row))
		if err != nil {
			return fmt.Errorf("%w: coins of %x: %v", errChainstateFormat, txid, err)
		}
		h.CoinsCount += uint64(len(vouts))
		return nil
	})
	if err != nil {
		return h, err
	}
	if onHeader != nil {
		onHeader(h)
	}
	return h, c.rows(func(txid [32]byte, vout uint32, row []byte) error {
		br.Reset(row)
		u.r.Reset(&br)
		read := u.chainstateCoins
		if c.txout {
			read = func(txid [32]byte, _ int, fn func(utxoRecord) error) error { return u.outputCoin(txid, vout, fn) }
		}
		if err := read(txid, len(row), fn); err != nil {
			return fmt.Errorf("%w: coins of %x: %v", errChainstateFormat, txid, err)
		}
		return nil
	})
}

// outputCoin reads a per-output row: the height and coinbase code, the amount, and the script, which a CashTokens output stores with its token data in front.
func (u *utxoReader) outputCoin(txid [32]byte, vout uint32, fn func(utxoRecord) error) error {
	rec, err := u.coin(txid, uint64(vout))
	if err != nil {
		return err
	}
	if _, err := u.r.ReadByte(); err != io.EOF {
		return errors.New("trailing bytes after the script")
	}
	if len(rec.ScriptPubKey) > 0 && rec.ScriptPubKey[0] == cashTokenPrefix {
		script := unwrapCashTokenScript(rec.ScriptPubKey)
		if len(script) == len(rec.ScriptPubKey) || len(script) == 0 {
			return errors.New("unreadable token prefix")
		}
		rec.ScriptPubKey = script
	}
	return fn(rec)
}

// coinsHeader reads a 1.14 CCoins row up to its outputs: the version, the code, and the availability mask.
// Each output takes at least two bytes, so a mask promising more than half the row's length is refused before anything is allocated for it.
func (u *utxoReader) coinsHeader(rowLen int) (vouts []uint32, coinbase bool, err error) {
	if _, err := u.varint(); err != nil {
		return nil, false, err
	}
	code, err := u.varint()
	if err != nil {
		return nil, false, err
	}
	if code&2 != 0 {
		vouts = append(vouts, 0)
	}
	if code&4 != 0 {
		vouts = append(vouts, 1)
	}
	// With neither of the first two outputs unspent the count is stored one lower, since some output must be.
	nonzero := code / 8
	if code&6 == 0 {
		nonzero++
	}
	for base := uint32(2); nonzero > 0; base += 8 {
		b, err := u.r.ReadByte()
		if err != nil {
			return nil, false, io.ErrUnexpectedEOF
		}
		for bit := uint32(0); bit < 8; bit++ {
			if b&(1<<bit) != 0 {
				vouts = append(vouts, base+bit)
			}
		}
		if len(vouts) > rowLen/2 {
			return nil, false, errors.New("more unspent outputs than the row can hold")
		}
		if b != 0 {
			nonzero--
		}
	}
	return vouts, code&1 == 1, nil
}

func (u *utxoReader) chainstateCoins(txid [32]byte, rowLen int, fn func(utxoRecord) error) error {
	vouts, coinbase, err := u.coinsHeader(rowLen)
	if err != nil {
		return err
	}
	coins := make([]utxoRecord, len(vouts))
	for i, vout := range vouts {
		comp, err := u.varint()
		if err != nil {
			return err
		}
		script, err := u.script()
		if err != nil {
			return err
		}
		coins[i] = utxoRecord{ScriptPubKey: script, Txid: txid, Vout: uint64(vout), Amount: decompressUtxoAmount(comp), Coinbase: coinbase}
	}
	height, err := u.varint()
	if err != nil {
		return err
	}
	if height > math.MaxUint32 {
		return fmt.Errorf("height %d out of range", height)
	}
	if _, err := u.r.ReadByte(); err != io.EOF {
		return errors.New("trailing bytes after the height")
	}
	for _, coin := range coins {
		coin.Height = uint32(height)
		if err := fn(coin); err != nil {
			return err
		}
	}
	return nil
}
