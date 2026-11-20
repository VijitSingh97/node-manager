package main

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
)

const cashTokenPrefix = 0xef

// A parsed block stays whole while it applies in pieces, at about 110 bytes of heap per coin operation (measured), so this keeps it near 130 MiB.
var utxoBlockOpsCap = 1_200_000

type rawReader struct {
	b   []byte
	off int
	err error
}

func (r *rawReader) take(n uint64) []byte {
	if r.err != nil {
		return nil
	}
	if n > uint64(len(r.b)-r.off) {
		r.err = fmt.Errorf("%w: truncated raw block", errUtxoBadBlock)
		return nil
	}
	out := r.b[r.off : r.off+int(n)]
	r.off += int(n)
	return out
}

func (r *rawReader) u32() uint32 {
	if b := r.take(4); b != nil {
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}

func (r *rawReader) u64() uint64 {
	if b := r.take(8); b != nil {
		return binary.LittleEndian.Uint64(b)
	}
	return 0
}

func (r *rawReader) compact() uint64 {
	n, rest, ok := readCompactSize(r.b[min(r.off, len(r.b)):])
	if r.err == nil && !ok {
		r.err = fmt.Errorf("%w: bad compact size", errUtxoBadBlock)
	}
	if r.err != nil {
		return 0
	}
	r.off = len(r.b) - len(rest)
	return n
}

// readCompactSize decodes a canonical CompactSize, as Bitcoin's ReadCompactSize requires.
func readCompactSize(b []byte) (uint64, []byte, bool) {
	if len(b) == 0 {
		return 0, nil, false
	}
	var n uint64
	var size int
	switch b[0] {
	case 0xfd:
		size = 2
	case 0xfe:
		size = 4
	case 0xff:
		size = 8
	default:
		return uint64(b[0]), b[1:], true
	}
	if len(b) < 1+size {
		return 0, nil, false
	}
	for i := size; i >= 1; i-- {
		n = n<<8 | uint64(b[i])
	}
	if floor := map[int]uint64{2: 0xfd, 4: 0x10000, 8: 0x100000000}[size]; n < floor {
		return 0, nil, false
	}
	return n, b[1+size:], true
}

// unwrapCashTokenScript strips a CashTokens prefix as Bitcoin Cash Node's UnwrapScriptPubKey does; an unparseable prefix leaves the whole script.
func unwrapCashTokenScript(b []byte) []byte {
	if len(b) == 0 || b[0] != cashTokenPrefix || len(b) < 34 {
		return b
	}
	field := b[33]
	structure, capability := field&0xf0, field&0x0f
	const amount, nft, commitment, reserved = 0x10, 0x20, 0x40, 0x80
	if structure == 0 || field&reserved != 0 || capability > 2 || structure&(nft|amount) == 0 ||
		structure&nft == 0 && (capability != 0 || structure&commitment != 0) {
		return b
	}
	rest := b[34:]
	if structure&commitment != 0 {
		n, after, ok := readCompactSize(rest)
		if !ok || n == 0 || n > 0x02000000 || n > uint64(len(after)) {
			return b
		}
		rest = after[n:]
	}
	if structure&amount != 0 {
		n, after, ok := readCompactSize(rest)
		if !ok || n == 0 || n > math.MaxInt64 {
			return b
		}
		rest = after
	}
	return rest
}

// A merge-mined Dogecoin block sets this version bit and carries its parent chain's proof between the header and the transactions.
const utxoAuxPowVersion = 1 << 8

// skipAuxPow reads past the proof: the parent coinbase, the parent block hash, the coinbase and chain merkle branches with their indexes, and the parent header.
func (r *rawReader) skipAuxPow() {
	r.skipTx()
	r.take(32)
	for range 2 {
		r.take(32 * r.count(32))
		r.take(4)
	}
	r.take(80)
}

// count reads a CompactSize that numbers items of at least size bytes, refusing one the rest of the block cannot hold.
func (r *rawReader) count(size uint64) uint64 {
	n := r.compact()
	if r.err == nil && n > uint64(len(r.b)-r.off)/size {
		r.err = fmt.Errorf("%w: %d items cannot fit in the block", errUtxoBadBlock, n)
		return 0
	}
	return n
}

// skipTx reads past a transaction that may use the witness serialization, as a parent chain's coinbase can.
func (r *rawReader) skipTx() {
	r.take(4)
	ins := r.count(41)
	witness := false
	if ins == 0 && r.err == nil {
		flags := r.take(1)
		if r.err != nil {
			return
		}
		if flags[0] == 0 {
			r.take(4)
			return
		}
		if flags[0] != 1 {
			r.err = fmt.Errorf("%w: unknown transaction flags %x", errUtxoBadBlock, flags[0])
			return
		}
		witness, ins = true, r.count(41)
	}
	for i := uint64(0); i < ins && r.err == nil; i++ {
		r.take(36)
		r.take(r.compact())
		r.take(4)
	}
	for i, outs := uint64(0), r.count(9); i < outs && r.err == nil; i++ {
		r.take(8)
		r.take(r.compact())
	}
	for i := uint64(0); witness && i < ins && r.err == nil; i++ {
		for j, items := uint64(0), r.count(1); j < items && r.err == nil; j++ {
			r.take(r.compact())
		}
	}
	r.take(4)
}

func doubleSHA256(b []byte) [32]byte {
	first := sha256.Sum256(b)
	return sha256.Sum256(first[:])
}

// parseRawBlock reads a raw block in the legacy transaction format; spends carry no script, so the store resolves them through its reverse keys.
func parseRawBlock(raw []byte, height uint32, chain *utxoChainParams) (utxoBlock, error) {
	blk := utxoBlock{Height: height}
	r := &rawReader{b: raw}
	header := r.take(80)
	if r.err != nil {
		return blk, r.err
	}
	blk.Hash = doubleSHA256(header)
	copy(blk.PrevHash[:], header[4:36])
	if chain.AuxPow && binary.LittleEndian.Uint32(header)&utxoAuxPowVersion != 0 {
		r.skipAuxPow()
	}
	txs := r.compact()
	ops := 0
	for t := uint64(0); t < txs && r.err == nil; t++ {
		start := r.off
		r.take(4)
		ins := r.compact()
		spends := make([]utxoSpend, 0, min(ins, 1<<16))
		for i := uint64(0); i < ins && r.err == nil; i++ {
			var sp utxoSpend
			copy(sp.Txid[:], r.take(32))
			sp.Vout = r.u32()
			r.take(r.compact())
			r.take(4)
			if t > 0 {
				spends = append(spends, sp)
			}
		}
		outs := r.compact()
		creates := make([]utxoCreate, 0, min(outs, 1<<16))
		for o := uint64(0); o < outs && r.err == nil; o++ {
			c := utxoCreate{Amount: r.u64()}
			c.Vout = uint32(o)
			c.Script = r.take(r.compact())
			if chain.CashTokens {
				c.Script = unwrapCashTokenScript(c.Script)
			}
			creates = append(creates, c)
		}
		r.take(4)
		if r.err != nil {
			break
		}
		ops += len(spends)
		if height > 0 {
			ops += len(creates)
		}
		if ops > utxoBlockOpsCap {
			continue
		}
		txid := doubleSHA256(raw[start:r.off])
		for i := range creates {
			creates[i].Txid = txid
		}
		blk.Spends = append(blk.Spends, spends...)
		if height > 0 {
			blk.Creates = append(blk.Creates, creates...)
		}
	}
	if r.err == nil && r.off != len(raw) {
		r.err = fmt.Errorf("%w: %d trailing bytes after the transactions", errUtxoBadBlock, len(raw)-r.off)
	}
	if r.err == nil && ops > utxoBlockOpsCap {
		return utxoBlock{}, fmt.Errorf("%w: block %d has %d coin operations, more than the %d that fit in the dashboard's memory", errUtxoBlockTooLarge, height, ops, utxoBlockOpsCap)
	}
	// Copy the scripts out so a queued block does not keep the whole raw block, input padding included, alive.
	size := 0
	for _, c := range blk.Creates {
		size += len(c.Script)
	}
	scripts := make([]byte, 0, size)
	for i := range blk.Creates {
		start := len(scripts)
		scripts = append(scripts, blk.Creates[i].Script...)
		blk.Creates[i].Script = scripts[start:len(scripts):len(scripts)]
	}
	return blk, r.err
}
