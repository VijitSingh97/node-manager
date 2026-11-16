package main

import (
	"bufio"
	"bytes"
	"container/heap"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sync/atomic"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/objstorage/objstorageprovider"
	"github.com/cockroachdb/pebble/sstable"
	"github.com/cockroachdb/pebble/vfs"
)

const (
	utxoIngestRecLen   = utxoKeyLen + utxoValueLen
	utxoIngestBufBytes = 128 << 10
	utxoIngestLevel    = 6
)

var (
	utxoIngestRunRecords uint64 = 2 << 20
	utxoIngestSSTBytes   uint64 = 128 << 20
)

type utxoIngestRec [utxoIngestRecLen]byte

// Written counts skipped coins too, so it reaches Total only when every coin is in an SST.
type utxoIngestProgress struct {
	Total   atomic.Uint64
	Parsed  atomic.Uint64
	Written atomic.Uint64

	Checkpoint func() error
}

const utxoIngestCheckpointEvery = 4096

func (p *utxoIngestProgress) checkpoint() error {
	if p.Checkpoint == nil {
		return nil
	}
	return p.Checkpoint()
}

func (p *utxoIngestProgress) Fraction() float64 {
	t := p.Total.Load()
	if t == 0 {
		return 0
	}
	return float64(p.Written.Load()) / float64(t)
}

// Reverse records share the 64-byte run format (key, value, zero padding); sorting on the first 52 bytes still orders them by key because keys are unique.
type utxoRun struct {
	path           string
	keyLen, valLen int
}

type utxoRunReader struct {
	utxoRun
	f   vfs.File
	r   *bufio.Reader
	cur utxoIngestRec
}

func (r *utxoRunReader) key() []byte { return r.cur[:r.keyLen] }

func (r *utxoRunReader) next() (bool, error) {
	_, err := io.ReadFull(r.r, r.cur[:])
	if err == io.EOF {
		return false, nil
	}
	return err == nil, err
}

type utxoRunHeap []*utxoRunReader

func (h utxoRunHeap) Len() int { return len(h) }
func (h utxoRunHeap) Less(i, j int) bool {
	return bytes.Compare(h[i].key(), h[j].key()) < 0
}
func (h utxoRunHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *utxoRunHeap) Push(x any)   { *h = append(*h, x.(*utxoRunReader)) }
func (h *utxoRunHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

func utxoCompareRec(a, b utxoIngestRec) int {
	return bytes.Compare(a[:utxoKeyLen], b[:utxoKeyLen])
}

func writeUtxoRun(fs vfs.FS, path string, recs []utxoIngestRec) error {
	slices.SortFunc(recs, utxoCompareRec)
	f, err := fs.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	for i := range recs {
		if _, err := w.Write(recs[i][:]); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func mergeUtxoRunsToSSTs(fs vfs.FS, tmp string, runs []utxoRun, prog *utxoIngestProgress) ([]string, error) {
	var h utxoRunHeap
	defer func() {
		for _, r := range h {
			_ = r.f.Close()
		}
	}()
	for _, run := range runs {
		f, err := fs.Open(run.path)
		if err != nil {
			return nil, err
		}
		r := &utxoRunReader{utxoRun: run, f: f, r: bufio.NewReaderSize(f, utxoIngestBufBytes)}
		ok, err := r.next()
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		if !ok {
			_ = f.Close()
			continue
		}
		h = append(h, r)
	}
	heap.Init(&h)
	wopts := (&pebble.Options{FormatMajorVersion: utxoPebbleFormat}).EnsureDefaults().
		MakeWriterOptions(utxoIngestLevel, utxoPebbleFormat.MaxTableFormat())
	var (
		ssts     []string
		w        *sstable.Writer
		prev     []byte
		havePrev bool
		pending  uint64
		perCoin  uint64 = 1
	)
	if slices.ContainsFunc(runs, func(r utxoRun) bool { return r.keyLen == utxoRevKeyLen }) {
		perCoin = 2
	}
	closeWriter := func() error {
		if w == nil {
			return nil
		}
		err := w.Close()
		w = nil
		return err
	}
	for len(h) > 0 {
		top := h[0]
		key, val := top.key(), top.cur[top.keyLen:top.keyLen+top.valLen]
		if havePrev && bytes.Compare(prev, key) >= 0 {
			_ = closeWriter()
			return ssts, errors.New("snapshot has a duplicate coin key")
		}
		prev = append(prev[:0], key...)
		havePrev = true
		if w != nil && w.EstimatedSize() >= utxoIngestSSTBytes {
			if err := closeWriter(); err != nil {
				return ssts, err
			}
		}
		if w == nil {
			path := fs.PathJoin(tmp, fmt.Sprintf("%06d.sst", len(ssts)))
			f, err := fs.Create(path)
			if err != nil {
				return ssts, err
			}
			ssts = append(ssts, path)
			w = sstable.NewWriter(objstorageprovider.NewFileWritable(f), wopts)
		}
		if err := w.Set(key, val); err != nil {
			_ = closeWriter()
			return ssts, err
		}
		if pending++; pending == utxoIngestCheckpointEvery {
			prog.Written.Add(pending / perCoin)
			pending = 0
			if err := prog.checkpoint(); err != nil {
				_ = closeWriter()
				return ssts, err
			}
		}
		ok, err := top.next()
		if err != nil {
			_ = closeWriter()
			return ssts, err
		}
		if ok {
			heap.Fix(&h, 0)
		} else {
			_ = top.f.Close()
			heap.Pop(&h)
		}
	}
	prog.Written.Add(pending / perCoin)
	return ssts, closeWriter()
}

// baseHeight runs after the merge so the tip comes from the RPC result, not the dump alone.
func ingestUtxoSnapshot(s *utxoStore, r io.Reader, chain *utxoChainParams, network [4]byte, baseHeight func(hash [32]byte) (uint32, error), prog *utxoIngestProgress) (utxoHeader, error) {
	if prog == nil {
		prog = &utxoIngestProgress{}
	}
	if err := s.RequireEmpty(); err != nil {
		return utxoHeader{}, err
	}
	fs := s.fs
	tmp := fs.PathJoin(s.dir, utxoIngestTmpDir)
	if err := fs.RemoveAll(tmp); err != nil {
		return utxoHeader{}, err
	}
	if err := fs.MkdirAll(tmp, 0o755); err != nil {
		return utxoHeader{}, err
	}
	defer func() { _ = fs.RemoveAll(tmp) }()

	var (
		buf, rbuf []utxoIngestRec
		runs      []utxoRun
	)
	write := func(recs []utxoIngestRec, keyLen, valLen int) error {
		path := fs.PathJoin(tmp, fmt.Sprintf("run-%06d", len(runs)))
		if err := writeUtxoRun(fs, path, recs); err != nil {
			return err
		}
		runs = append(runs, utxoRun{path, keyLen, valLen})
		return nil
	}
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		if err := write(buf, utxoKeyLen, utxoValueLen); err != nil {
			return err
		}
		if s.reverse {
			if err := write(rbuf, utxoRevKeyLen, utxoPrefixLen); err != nil {
				return err
			}
		}
		buf, rbuf = buf[:0], rbuf[:0]
		return nil
	}
	runRecords := utxoIngestRunRecords
	if s.reverse {
		runRecords = max(1, runRecords/2)
	}
	hdr, err := chain.parseDump(r, network, func(h utxoHeader) {
		prog.Total.Store(h.CoinsCount)
		buf = make([]utxoIngestRec, 0, min(h.CoinsCount, runRecords))
		if s.reverse {
			rbuf = make([]utxoIngestRec, 0, cap(buf))
		}
	}, func(rec utxoRecord) error {
		if prog.Parsed.Add(1)%utxoIngestCheckpointEvery == 0 {
			if err := prog.checkpoint(); err != nil {
				return err
			}
		}
		if rec.Vout > math.MaxUint32 {
			return fmt.Errorf("output index %d out of range", rec.Vout)
		}
		if !utxoSpendable(rec.ScriptPubKey) {
			prog.Written.Add(1)
			return nil
		}
		var e utxoIngestRec
		key := utxoCoinKey(utxoScriptHash(rec.ScriptPubKey), utxoOutpoint{Txid: rec.Txid, Vout: uint32(rec.Vout)})
		copy(e[:], key)
		copy(e[utxoKeyLen:], utxoCoinValue(rec.Amount, rec.Height))
		buf = append(buf, e)
		if s.reverse {
			var r utxoIngestRec
			copy(r[:], utxoRevKey(key))
			copy(r[utxoRevKeyLen:], key[:utxoPrefixLen])
			rbuf = append(rbuf, r)
		}
		if len(buf) == cap(buf) {
			return flush()
		}
		return nil
	})
	if err != nil {
		return hdr, err
	}
	if err := flush(); err != nil {
		return hdr, err
	}
	buf, rbuf = nil, nil
	ssts, err := mergeUtxoRunsToSSTs(fs, tmp, runs, prog)
	if err != nil {
		return hdr, err
	}
	for _, run := range runs {
		_ = fs.Remove(run.path)
	}
	height, err := baseHeight(hdr.BaseHash)
	if err != nil {
		return hdr, err
	}
	return hdr, s.IngestFiles(ssts, height, hdr.BaseHash)
}
