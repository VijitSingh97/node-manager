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
}

func (p *utxoIngestProgress) Fraction() float64 {
	t := p.Total.Load()
	if t == 0 {
		return 0
	}
	return float64(p.Written.Load()) / float64(t)
}

type utxoRunReader struct {
	f   vfs.File
	r   *bufio.Reader
	cur utxoIngestRec
}

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
	return bytes.Compare(h[i].cur[:utxoKeyLen], h[j].cur[:utxoKeyLen]) < 0
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

func mergeUtxoRunsToSSTs(fs vfs.FS, tmp string, runs []string, prog *utxoIngestProgress) ([]string, error) {
	var h utxoRunHeap
	defer func() {
		for _, r := range h {
			_ = r.f.Close()
		}
	}()
	for _, p := range runs {
		f, err := fs.Open(p)
		if err != nil {
			return nil, err
		}
		r := &utxoRunReader{f: f, r: bufio.NewReaderSize(f, utxoIngestBufBytes)}
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
		prev     [utxoKeyLen]byte
		havePrev bool
		pending  uint64
	)
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
		key, val := top.cur[:utxoKeyLen], top.cur[utxoKeyLen:]
		if havePrev && bytes.Compare(prev[:], key) >= 0 {
			_ = closeWriter()
			return ssts, errors.New("snapshot has a duplicate coin key")
		}
		copy(prev[:], key)
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
		if pending++; pending == 4096 {
			prog.Written.Add(pending)
			pending = 0
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
	prog.Written.Add(pending)
	return ssts, closeWriter()
}

// ingestUtxoSnapshot loads a dumptxoutset v2 stream into an empty store; the caller supplies the base block height, which the dump does not carry.
func ingestUtxoSnapshot(s *utxoStore, r io.Reader, network [4]byte, height uint32, prog *utxoIngestProgress) (utxoHeader, error) {
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
		buf  []utxoIngestRec
		runs []string
	)
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		path := fs.PathJoin(tmp, fmt.Sprintf("run-%06d", len(runs)))
		if err := writeUtxoRun(fs, path, buf); err != nil {
			return err
		}
		runs = append(runs, path)
		buf = buf[:0]
		return nil
	}
	hdr, err := parseUtxoDumpHeader(r, network, func(h utxoHeader) {
		prog.Total.Store(h.CoinsCount)
		buf = make([]utxoIngestRec, 0, min(h.CoinsCount, utxoIngestRunRecords))
	}, func(rec utxoRecord) error {
		prog.Parsed.Add(1)
		if rec.Vout > math.MaxUint32 {
			return fmt.Errorf("output index %d out of range", rec.Vout)
		}
		if !utxoSpendable(rec.ScriptPubKey) {
			prog.Written.Add(1)
			return nil
		}
		var e utxoIngestRec
		copy(e[:], utxoCoinKey(utxoScriptHash(rec.ScriptPubKey), utxoOutpoint{Txid: rec.Txid, Vout: uint32(rec.Vout)}))
		copy(e[utxoKeyLen:], utxoCoinValue(rec.Amount, rec.Height))
		buf = append(buf, e)
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
	buf = nil
	ssts, err := mergeUtxoRunsToSSTs(fs, tmp, runs, prog)
	if err != nil {
		return hdr, err
	}
	for _, p := range runs {
		_ = fs.Remove(p)
	}
	return hdr, s.IngestFiles(ssts, height, hdr.BaseHash)
}
