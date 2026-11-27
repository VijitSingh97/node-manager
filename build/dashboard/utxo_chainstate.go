package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/record"
	"github.com/cockroachdb/pebble/vfs"
)

var errChainstateFormat = errors.New("unexpected chainstate format")

// The key LevelDB-backed Bitcoin Core forks store their 8-byte XOR key under: a serialized string, length byte first.
var chainstateObfuscateKey = append([]byte{14, 0}, "obfuscate_key"...)

// The copy sits on the dashboard's read-only mount of the node's data, so it is opened without taking a lock.
type chainstateFS struct {
	vfs.FS
	logs *[]string
}

func (chainstateFS) Lock(string) (io.Closer, error) { return io.NopCloser(nil), nil }

// Pebble replays a log into memory, several times the size of a Dogecoin node's shutdown flush, so it reads an empty file and replayLogs streams the log to disk.
func (fs chainstateFS) Open(name string, opts ...vfs.OpenOption) (vfs.File, error) {
	if strings.HasSuffix(name, ".log") {
		*fs.logs = append(*fs.logs, name)
		return fs.FS.Open(os.DevNull)
	}
	return fs.FS.Open(name, opts...)
}

// A tampered log cannot make one key or row allocate more than this.
const chainstateMaxLogField = 32 << 20

type chainstateLogger struct{ fatal *atomic.Pointer[string] }

func (chainstateLogger) Infof(string, ...interface{}) {}

func (l chainstateLogger) Fatalf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	l.fatal.CompareAndSwap(nil, &msg)
}

// chainstateCopy reads a stopped node's chainstate, copied by the host helper with its LevelDB tables renamed to .sst for Pebble.
type chainstateCopy struct {
	db *pebble.DB
	// log holds the logs' rows, newer than the tables', each put behind a 1 byte so that an empty value marks a delete.
	log    *pebble.DB
	logFS  vfs.FS
	logDir string
	xor    []byte
	Best   [32]byte
	// txout selects the per-output rows of Bitcoin Core 0.15 and its descendants over the per-transaction rows of Core 0.14 and Dogecoin 1.14.
	txout bool
	fatal atomic.Pointer[string]
}

func openChainstateCopy(dir string, logFS vfs.FS, logDir string) (*chainstateCopy, error) {
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
	c := &chainstateCopy{logFS: logFS, logDir: logDir}
	var logs []string
	db, err := pebble.Open(dir, &pebble.Options{ReadOnly: true, FS: chainstateFS{vfs.Default, &logs}, Logger: chainstateLogger{&c.fatal}})
	if err != nil {
		return nil, fmt.Errorf("open chainstate copy: %w", err)
	}
	c.db = db
	// Any early return, a panic included, closes the copy so no descriptor pins its tables.
	opened := false
	defer func() {
		if !opened {
			_ = c.Close()
		}
	}()
	if err := logFS.RemoveAll(logDir); err != nil {
		return nil, err
	}
	c.log, err = pebble.Open(logDir, &pebble.Options{DisableWAL: true, FS: logFS, Logger: chainstateLogger{&c.fatal}})
	if err != nil {
		return nil, fmt.Errorf("open chainstate log store: %w", err)
	}
	if err := c.replayLogs(logs); err != nil {
		return nil, err
	}
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

func (c *chainstateCopy) Close() error {
	err := c.db.Close()
	if c.log != nil {
		err = errors.Join(err, c.log.Close())
	}
	return errors.Join(err, c.logFS.RemoveAll(c.logDir))
}

// replayLogs applies the logs Pebble chose, oldest first, a batch at a time; like Pebble, it stops at an invalid record that starts a log's tail.
func (c *chainstateCopy) replayLogs(logs []string) error {
	b := c.log.NewBatch()
	defer b.Close()
	var key, val []byte
	for _, name := range logs {
		num, err := strconv.ParseUint(strings.TrimSuffix(filepath.Base(name), ".log"), 10, 64)
		if err != nil {
			return fmt.Errorf("%w: log %s", errChainstateFormat, name)
		}
		f, err := os.Open(name)
		if err != nil {
			return fmt.Errorf("open chainstate log: %w", err)
		}
		rr := record.NewReader(f, pebble.FileNum(num))
		for {
			r, err := rr.Next()
			if err == io.EOF || record.IsInvalidRecord(err) {
				break
			}
			if err == nil {
				key, val, err = c.replayBatch(bufio.NewReader(r), b, key, val)
			}
			if err != nil {
				_ = f.Close()
				return fmt.Errorf("%w: log %s: %v", errChainstateFormat, filepath.Base(name), err)
			}
		}
		_ = f.Close()
	}
	return b.Commit(pebble.NoSync)
}

// replayBatch streams one LevelDB write batch: a sequence number, a count, then puts and deletes.
func (c *chainstateCopy) replayBatch(r *bufio.Reader, b *pebble.Batch, key, val []byte) ([]byte, []byte, error) {
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return key, val, err
	}
	field := func(buf []byte) ([]byte, error) {
		n, err := binary.ReadUvarint(r)
		if err != nil {
			return buf, err
		}
		if n > chainstateMaxLogField {
			return buf, fmt.Errorf("field of %d bytes", n)
		}
		at := len(buf)
		buf = slices.Grow(buf, int(n))[:at+int(n)]
		_, err = io.ReadFull(r, buf[at:])
		return buf, err
	}
	for range binary.LittleEndian.Uint32(hdr[8:]) {
		kind, err := r.ReadByte()
		if err == nil {
			key, err = field(key[:0])
		}
		switch {
		case err != nil:
			return key, val, err
		case kind == byte(pebble.InternalKeyKindSet):
			if val, err = field(append(val[:0], 1)); err == nil {
				err = b.Set(key, val, nil)
			}
		case kind == byte(pebble.InternalKeyKindDelete):
			err = b.Set(key, nil, nil)
		default:
			err = fmt.Errorf("record kind %d", kind)
		}
		if err == nil && b.Len() >= 1<<20 {
			err = b.Commit(pebble.NoSync)
			b.Reset()
		}
		if err != nil {
			return key, val, err
		}
	}
	switch _, err := r.ReadByte(); {
	case err == nil:
		return key, val, errors.New("trailing bytes after the batch")
	case err != io.EOF:
		return key, val, err
	}
	return key, val, nil
}

func (c *chainstateCopy) get(key []byte) ([]byte, error) {
	v, closer, err := c.log.Get(key)
	fromLog := err == nil
	if errors.Is(err, pebble.ErrNotFound) {
		v, closer, err = c.db.Get(key)
	}
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	if fromLog {
		if len(v) == 0 {
			return nil, pebble.ErrNotFound
		}
		v = v[1:]
	}
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
	bounds := &pebble.IterOptions{LowerBound: []byte{prefix}, UpperBound: []byte{prefix + 1}}
	it, err := c.db.NewIter(bounds)
	if err != nil {
		return err
	}
	lt, err := c.log.NewIter(bounds)
	if err != nil {
		_ = it.Close()
		return err
	}
	if err := c.mergeRows(it, lt, fn); err != nil {
		_, _ = it.Close(), lt.Close()
		return err
	}
	if err := errors.Join(it.Close(), lt.Close()); err != nil {
		return err
	}
	if msg := c.fatal.Load(); msg != nil {
		return fmt.Errorf("chainstate copy: %s", *msg)
	}
	return nil
}

// mergeRows walks the tables and the logs' rows in key order, a log's row replacing or deleting the table's.
func (c *chainstateCopy) mergeRows(it, lt *pebble.Iterator, fn func(txid [32]byte, vout uint32, row []byte) error) error {
	var row []byte
	it.First()
	lt.First()
	for it.Valid() || lt.Valid() {
		src := it
		if lt.Valid() && (!it.Valid() || bytes.Compare(lt.Key(), it.Key()) <= 0) {
			if it.Valid() && bytes.Equal(lt.Key(), it.Key()) {
				it.Next()
			}
			src = lt
		}
		v := src.Value()
		if src == lt {
			if len(v) == 0 {
				lt.Next()
				continue
			}
			v = v[1:]
		}
		txid, vout, ok := c.splitKey(src.Key())
		if !ok {
			return fmt.Errorf("%w: coins key of %d bytes", errChainstateFormat, len(src.Key()))
		}
		row = append(row[:0], v...)
		c.unmask(row)
		if err := fn(txid, vout, row); err != nil {
			return err
		}
		src.Next()
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
		// Like the block follower, a prefix that is not valid token data stays part of the script, as for outputs made before tokens existed.
		rec.ScriptPubKey = unwrapCashTokenScript(rec.ScriptPubKey)
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
