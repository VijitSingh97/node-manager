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
	db    *pebble.DB
	xor   []byte
	Best  [32]byte
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
	key, err := c.get(chainstateObfuscateKey)
	switch {
	case errors.Is(err, pebble.ErrNotFound):
	case err != nil:
		_ = db.Close()
		return nil, err
	case len(key) != 9 || key[0] != 8:
		_ = db.Close()
		return nil, fmt.Errorf("%w: obfuscation key of %d bytes", errChainstateFormat, len(key))
	default:
		c.xor = key[1:]
	}
	best, err := c.get([]byte{'B'})
	if err != nil || len(best) != 32 {
		_ = db.Close()
		return nil, fmt.Errorf("%w: no best block (%v)", errChainstateFormat, err)
	}
	c.unmask(best)
	copy(c.Best[:], best)
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

// rows calls fn for each transaction's coins row, unmasked; the slice is reused between calls.
func (c *chainstateCopy) rows(fn func(txid [32]byte, row []byte) error) error {
	it, err := c.db.NewIter(&pebble.IterOptions{LowerBound: []byte{'c'}, UpperBound: []byte{'d'}})
	if err != nil {
		return err
	}
	var row []byte
	for it.First(); it.Valid(); it.Next() {
		key := it.Key()
		if len(key) != 33 {
			_ = it.Close()
			return fmt.Errorf("%w: coins key of %d bytes", errChainstateFormat, len(key))
		}
		var txid [32]byte
		copy(txid[:], key[1:])
		row = append(row[:0], it.Value()...)
		c.unmask(row)
		if err := fn(txid, row); err != nil {
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

// parse counts the coins first, so the ingest can size its runs and report progress, then streams them.
func (c *chainstateCopy) parse(onHeader func(utxoHeader), fn func(utxoRecord) error) (utxoHeader, error) {
	u := &utxoReader{r: bufio.NewReaderSize(nil, 4096), oversizeUnspendable: true}
	var br bytes.Reader
	h := utxoHeader{BaseHash: c.Best}
	err := c.rows(func(txid [32]byte, row []byte) error {
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
	return h, c.rows(func(txid [32]byte, row []byte) error {
		br.Reset(row)
		u.r.Reset(&br)
		if err := u.chainstateCoins(txid, len(row), fn); err != nil {
			return fmt.Errorf("%w: coins of %x: %v", errChainstateFormat, txid, err)
		}
		return nil
	})
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
