package main

import (
	"bufio"
	"bytes"
	"container/heap"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/bloom"
	"github.com/cockroachdb/pebble/objstorage/objstorageprovider"
	"github.com/cockroachdb/pebble/sstable"
	"github.com/cockroachdb/pebble/vfs"
)

const (
	prefixLen = 8
	keyLen    = prefixLen + 32 + 4
	valLen    = 8 + 4
	recLen    = keyLen + valLen
	sampleMax = 200000
)

var (
	n          = flag.Int("n", 10_000_000, "number of entries")
	dir        = flag.String("dir", "data", "working directory (removed with -clean)")
	cacheMB    = flag.Int64("cache-mb", 64, "pebble block cache MiB")
	chunk      = flag.Int("chunk", 2_000_000, "entries per in-memory sort run")
	sstMB      = flag.Int64("sst-mb", 128, "target SST size MiB")
	lookups    = flag.Int("lookups", 100_000, "lookups per phase")
	warmup     = flag.Int("warmup", 200_000, "warm-up lookups before the warm phase")
	timeLimit  = flag.Duration("time-limit", 0, "abort after this long (0 = none)")
	maxDiskGB  = flag.Float64("max-disk-gb", 0, "refuse to start if estimated peak disk exceeds this (0 = none)")
	maxTempC   = flag.Float64("max-temp-c", 88, "abort at or above this thermal zone temperature")
	clean      = flag.Bool("clean", false, "delete the working directory when done")
	skipIngest = flag.Bool("skip-ingest", false, "reuse an existing database in -dir")
)

func main() {
	flag.Parse()
	start := time.Now()
	est := float64(*n) * recLen * 2 * 1.3 / 1e9
	if *maxDiskGB > 0 && est > *maxDiskGB {
		fatal(fmt.Sprintf("estimated peak disk %.1f GB exceeds budget %.1f GB", est, *maxDiskGB))
	}
	go guard(start)
	runs := filepath.Join(*dir, "runs")
	sst := filepath.Join(*dir, "sst")
	dbDir := filepath.Join(*dir, "db")
	check(os.MkdirAll(runs, 0o755))
	check(os.MkdirAll(sst, 0o755))

	fmt.Printf("entries=%d key=%dB value=%dB cache=%dMiB GOMAXPROCS=%d go=%s\n", *n, keyLen, valLen, *cacheMB, runtime.GOMAXPROCS(0), runtime.Version())
	var sample [][]byte
	if !*skipIngest {
		t0 := time.Now()
		runFiles := genRuns(runs)
		fmt.Printf("generate+sort runs: %s (%d runs)\n", time.Since(t0).Round(time.Millisecond), len(runFiles))
		t1 := time.Now()
		files, runBytes, sstBytes := mergeToSSTs(runFiles, sst, &sample)
		fmt.Printf("merge to sorted SSTs: %s (%d files)\n", time.Since(t1).Round(time.Millisecond), len(files))
		fmt.Printf("peak temp disk (runs+ssts, before run cleanup): %s\n", human(runBytes+sstBytes))
		check(os.RemoveAll(runs))
		t2 := time.Now()
		db := openDB(dbDir)
		check(db.Ingest(files))
		fmt.Printf("ingest: %s\n", time.Since(t2).Round(time.Millisecond))
		check(db.Close())
		fmt.Printf("ingest pipeline total wall: %s\n", time.Since(t0).Round(time.Millisecond))
		check(os.RemoveAll(sst))
		fmt.Printf("final db size on disk: %s (%.1f B/entry)\n", human(dirSize(dbDir)), float64(dirSize(dbDir))/float64(*n))
		fmt.Printf("peak RSS after ingest: %s\n", human(maxRSS()))
	} else {
		sample = resample()
	}

	dropCache(dbDir)
	db := openDB(dbDir)
	rng := rand.New(rand.NewSource(7))
	report("cold prefix", prefixPhase(db, sample, rng, *lookups))
	report("cold point ", pointPhase(db, sample, rng, *lookups))
	prefixPhase(db, sample, rng, *warmup)
	pointPhase(db, sample, rng, *warmup)
	report("warm prefix", prefixPhase(db, sample, rng, *lookups))
	report("warm point ", pointPhase(db, sample, rng, *lookups))
	m := db.Metrics()
	fmt.Printf("block cache hit rate: %.1f%%\n", 100*float64(m.BlockCache.Hits)/float64(m.BlockCache.Hits+m.BlockCache.Misses+1))
	check(db.Close())
	fmt.Printf("peak RSS overall: %s\n", human(maxRSS()))
	fmt.Printf("total wall: %s\n", time.Since(start).Round(time.Millisecond))
	if *clean {
		check(os.RemoveAll(*dir))
	}
}

func splitmix(x *uint64) uint64 {
	*x += 0x9e3779b97f4a7c15
	z := *x
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func makeRec(i uint64, buf []byte) {
	s := i*0x2545f4914f6cdd1d + 1
	addr := splitmix(&s) % uint64(*n/2+1)
	binary.BigEndian.PutUint64(buf[0:], splitmix(&addr))
	for j := 0; j < 4; j++ {
		binary.BigEndian.PutUint64(buf[8+8*j:], splitmix(&s))
	}
	binary.BigEndian.PutUint32(buf[40:], uint32(splitmix(&s)%8))
	binary.BigEndian.PutUint64(buf[keyLen:], splitmix(&s)%2_100_000_000_000_000)
	binary.BigEndian.PutUint32(buf[keyLen+8:], uint32(splitmix(&s)%900_000))
}

func genRuns(runs string) []string {
	var names []string
	buf := make([]byte, *chunk*recLen)
	idx := make([]int, *chunk)
	var i uint64
	for int(i) < *n {
		c := min(*chunk, *n-int(i))
		for j := 0; j < c; j++ {
			makeRec(i+uint64(j), buf[j*recLen:(j+1)*recLen])
			idx[j] = j
		}
		ix := idx[:c]
		sort.Slice(ix, func(a, b int) bool {
			return bytes.Compare(buf[ix[a]*recLen:ix[a]*recLen+keyLen], buf[ix[b]*recLen:ix[b]*recLen+keyLen]) < 0
		})
		name := filepath.Join(runs, fmt.Sprintf("run-%05d", len(names)))
		f, err := os.Create(name)
		check(err)
		bw := bufio.NewWriterSize(f, 1<<20)
		for _, j := range ix {
			_, err := bw.Write(buf[j*recLen : (j+1)*recLen])
			check(err)
		}
		check(bw.Flush())
		check(f.Close())
		names = append(names, name)
		i += uint64(c)
	}
	return names
}

type src struct {
	r   io.Reader
	rec [recLen]byte
}

type mh []*src

func (h mh) Len() int           { return len(h) }
func (h mh) Less(a, b int) bool { return bytes.Compare(h[a].rec[:keyLen], h[b].rec[:keyLen]) < 0 }
func (h mh) Swap(a, b int)      { h[a], h[b] = h[b], h[a] }
func (h *mh) Push(x any)        { *h = append(*h, x.(*src)) }
func (h *mh) Pop() any          { o := *h; x := o[len(o)-1]; *h = o[:len(o)-1]; return x }

func (s *src) next() bool {
	_, err := io.ReadFull(s.r, s.rec[:])
	return err == nil
}

func openDB(path string) *pebble.DB {
	db, err := pebble.Open(path, dbOptions())
	check(err)
	return db
}

func dbOptions() *pebble.Options {
	cmp := *pebble.DefaultComparer
	cmp.Name = "utxo.prefix8"
	cmp.Split = func(k []byte) int { return min(prefixLen, len(k)) }
	o := &pebble.Options{
		Comparer:     &cmp,
		Cache:        pebble.NewCache(*cacheMB << 20),
		MaxOpenFiles: 512,
	}
	o.EnsureDefaults()
	for i := range o.Levels {
		o.Levels[i].FilterPolicy = bloom.FilterPolicy(10)
		o.Levels[i].FilterType = pebble.TableFilter
	}
	return o
}

func mergeToSSTs(runFiles []string, sstDir string, sample *[][]byte) ([]string, int64, int64) {
	var h mh
	var runBytes int64
	for _, rf := range runFiles {
		f, err := os.Open(rf)
		check(err)
		st, _ := f.Stat()
		runBytes += st.Size()
		s := &src{r: bufio.NewReaderSize(f, 1<<20)}
		if s.next() {
			h = append(h, s)
		}
	}
	heap.Init(&h)
	o := dbOptions()
	wo := o.MakeWriterOptions(6, o.FormatMajorVersion.MaxTableFormat())
	var (
		files []string
		w     *sstable.Writer
		wf    string
		wrote int64
		sstB  int64
		total int
	)
	rng := rand.New(rand.NewSource(3))
	finish := func() {
		if w == nil {
			return
		}
		check(w.Close())
		st, _ := os.Stat(wf)
		sstB += st.Size()
		files = append(files, wf)
		w = nil
	}
	for h.Len() > 0 {
		s := h[0]
		if w == nil {
			wf = filepath.Join(sstDir, fmt.Sprintf("%06d.sst", len(files)))
			f, err := vfs.Default.Create(wf)
			check(err)
			w = sstable.NewWriter(objstorageprovider.NewFileWritable(f), wo)
			wrote = 0
		}
		check(w.Set(s.rec[:keyLen], s.rec[keyLen:]))
		wrote += recLen
		if len(*sample) < sampleMax {
			*sample = append(*sample, append([]byte(nil), s.rec[:keyLen]...))
		} else if j := rng.Intn(total + 1); j < sampleMax {
			(*sample)[j] = append((*sample)[j][:0], s.rec[:keyLen]...)
		}
		total++
		if wrote >= *sstMB<<20 {
			finish()
		}
		if s.next() {
			heap.Fix(&h, 0)
		} else {
			heap.Pop(&h)
		}
	}
	finish()
	return files, runBytes, sstB
}

func resample() [][]byte {
	out := make([][]byte, 0, sampleMax)
	buf := make([]byte, recLen)
	for i := 0; i < sampleMax && i < *n; i++ {
		makeRec(uint64(i)*uint64(max(1, *n/sampleMax)), buf)
		out = append(out, append([]byte(nil), buf[:keyLen]...))
	}
	return out
}

func prefixPhase(db *pebble.DB, sample [][]byte, rng *rand.Rand, cnt int) []time.Duration {
	lat := make([]time.Duration, cnt)
	for i := range lat {
		p := sample[rng.Intn(len(sample))][:prefixLen]
		t := time.Now()
		it, err := db.NewIter(nil)
		check(err)
		found := 0
		for ok := it.SeekPrefixGE(p); ok && bytes.HasPrefix(it.Key(), p); ok = it.Next() {
			found++
		}
		check(it.Close())
		lat[i] = time.Since(t)
		if found == 0 {
			fatal("prefix lookup found nothing for an existing prefix")
		}
	}
	return lat
}

func pointPhase(db *pebble.DB, sample [][]byte, rng *rand.Rand, cnt int) []time.Duration {
	lat := make([]time.Duration, cnt)
	for i := range lat {
		k := sample[rng.Intn(len(sample))]
		t := time.Now()
		v, c, err := db.Get(k)
		check(err)
		if len(v) != valLen {
			fatal("point lookup returned wrong value")
		}
		c.Close()
		lat[i] = time.Since(t)
	}
	return lat
}

func report(name string, lat []time.Duration) {
	sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
	q := func(p float64) time.Duration { return lat[int(p*float64(len(lat)-1))] }
	fmt.Printf("%s n=%d p50=%s p90=%s p99=%s p99.9=%s max=%s\n", name, len(lat), q(.5), q(.9), q(.99), q(.999), lat[len(lat)-1])
}

func maxRSS() int64 {
	var r syscall.Rusage
	check(syscall.Getrusage(syscall.RUSAGE_SELF, &r))
	if runtime.GOOS == "darwin" {
		return r.Maxrss
	}
	return r.Maxrss << 10
}

func dirSize(p string) int64 {
	var s int64
	filepath.Walk(p, func(_ string, i os.FileInfo, err error) error {
		if err == nil && !i.IsDir() {
			s += i.Size()
		}
		return nil
	})
	return s
}

func human(b int64) string {
	return fmt.Sprintf("%.2f GiB", float64(b)/(1<<30))
}

func guard(start time.Time) {
	for range time.Tick(5 * time.Second) {
		if *timeLimit > 0 && time.Since(start) > *timeLimit {
			fatal("time limit reached")
		}
		if t := maxTemp(); t >= *maxTempC {
			fatal(fmt.Sprintf("thermal abort: %.1f C >= %.1f C", t, *maxTempC))
		}
	}
}

func maxTemp() float64 {
	m, _ := filepath.Glob("/sys/class/thermal/thermal_zone*/temp")
	var best float64
	for _, f := range m {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var v float64
		fmt.Sscan(strings.TrimSpace(string(b)), &v)
		best = max(best, v/1000)
	}
	return best
}

func fatal(msg string) {
	fmt.Println("ABORT:", msg)
	fmt.Printf("peak RSS: %s\n", human(maxRSS()))
	if *clean {
		os.RemoveAll(*dir)
	}
	os.Exit(3)
}

func check(err error) {
	if err != nil {
		fatal(err.Error())
	}
}
