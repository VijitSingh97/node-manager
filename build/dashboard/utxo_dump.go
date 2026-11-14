package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
)

const (
	utxoDumpVersion   = 2
	utxoMaxScriptSize = 10000
)

var (
	utxoDumpMagic    = []byte{'u', 't', 'x', 'o', 0xff}
	utxoNetMainnet   = [4]byte{0xf9, 0xbe, 0xb4, 0xd9}
	utxoNetRegtest   = [4]byte{0xfa, 0xbf, 0xb5, 0xda}
	secp256k1P, _    = new(big.Int).SetString("fffffffffffffffffffffffffffffffffffffffffffffffffffffffefffffc2f", 16)
	secp256k1Seven   = big.NewInt(7)
	secp256k1SqrtExp = new(big.Int).Rsh(new(big.Int).Add(secp256k1P, big.NewInt(1)), 2)
)

type utxoHeader struct {
	Network    [4]byte
	BaseHash   [32]byte
	CoinsCount uint64
}

type utxoRecord struct {
	ScriptPubKey []byte
	Txid         [32]byte
	Vout         uint64
	Amount       uint64
	Height       uint32
	Coinbase     bool
}

func (r utxoRecord) TxidHex() string {
	var rev [32]byte
	for i, b := range r.Txid {
		rev[31-i] = b
	}
	return hex.EncodeToString(rev[:])
}

type utxoReader struct {
	r   *bufio.Reader
	buf [32]byte
}

func (u *utxoReader) full(p []byte) error {
	_, err := io.ReadFull(u.r, p)
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

func (u *utxoReader) varint() (uint64, error) {
	var n uint64
	for {
		b, err := u.r.ReadByte()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return 0, err
		}
		if n > (1<<64-1)>>7 {
			return 0, errors.New("varint overflows uint64")
		}
		n = n<<7 | uint64(b&0x7f)
		if b&0x80 == 0 {
			return n, nil
		}
		if n == 1<<64-1 {
			return 0, errors.New("varint overflows uint64")
		}
		n++
	}
}

func (u *utxoReader) compactSize() (uint64, error) {
	if err := u.full(u.buf[:1]); err != nil {
		return 0, err
	}
	tag := u.buf[0]
	if tag < 253 {
		return uint64(tag), nil
	}
	width := map[byte]int{253: 2, 254: 4, 255: 8}[tag]
	var wide [8]byte
	if err := u.full(wide[:width]); err != nil {
		return 0, err
	}
	n := binary.LittleEndian.Uint64(wide[:])
	if min := map[byte]uint64{253: 253, 254: 0x10000, 255: 0x100000000}[tag]; n < min {
		return 0, errors.New("non-canonical CompactSize")
	}
	return n, nil
}

func decompressUtxoAmount(x uint64) uint64 {
	if x == 0 {
		return 0
	}
	x--
	e := x % 10
	x /= 10
	var n uint64
	if e < 9 {
		d := x%9 + 1
		x /= 9
		n = x*10 + d
	} else {
		n = x + 1
	}
	for ; e > 0; e-- {
		n *= 10
	}
	return n
}

func decompressUtxoPubkey(tag byte, x []byte) ([]byte, error) {
	xi := new(big.Int).SetBytes(x)
	rhs := new(big.Int).Exp(xi, big.NewInt(3), secp256k1P)
	rhs.Add(rhs, secp256k1Seven).Mod(rhs, secp256k1P)
	y := new(big.Int).Exp(rhs, secp256k1SqrtExp, secp256k1P)
	if new(big.Int).Exp(y, big.NewInt(2), secp256k1P).Cmp(rhs) != 0 {
		return nil, fmt.Errorf("pubkey x %x is not on the curve", x)
	}
	if (tag == 3) != (y.Bit(0) == 1) {
		y.Sub(secp256k1P, y)
	}
	out := make([]byte, 65)
	out[0] = 4
	copy(out[1:33], x)
	y.FillBytes(out[33:])
	return out, nil
}

func (u *utxoReader) script() ([]byte, error) {
	size, err := u.varint()
	if err != nil {
		return nil, err
	}
	switch {
	case size <= 1:
		if err := u.full(u.buf[:20]); err != nil {
			return nil, err
		}
		if size == 0 {
			return append(append([]byte{0x76, 0xa9, 20}, u.buf[:20]...), 0x88, 0xac), nil
		}
		return append(append([]byte{0xa9, 20}, u.buf[:20]...), 0x87), nil
	case size <= 5:
		if err := u.full(u.buf[:32]); err != nil {
			return nil, err
		}
		if size <= 3 {
			return append(append([]byte{33, byte(size)}, u.buf[:32]...), 0xac), nil
		}
		key, err := decompressUtxoPubkey(byte(size-2), u.buf[:32])
		if err != nil {
			return nil, err
		}
		return append(append([]byte{65}, key...), 0xac), nil
	}
	size -= 6
	if size > utxoMaxScriptSize {
		return nil, fmt.Errorf("script of %d bytes exceeds %d", size, utxoMaxScriptSize)
	}
	s := make([]byte, size)
	if err := u.full(s); err != nil {
		return nil, err
	}
	return s, nil
}

func readUtxoHeader(u *utxoReader, wantNetwork [4]byte) (utxoHeader, error) {
	var h utxoHeader
	var fixed [51]byte
	if err := u.full(fixed[:]); err != nil {
		return h, fmt.Errorf("utxo dump header: %w", err)
	}
	if !bytes.Equal(fixed[:5], utxoDumpMagic) {
		return h, fmt.Errorf("not a utxo dump: bad magic %x", fixed[:5])
	}
	if v := binary.LittleEndian.Uint16(fixed[5:7]); v != utxoDumpVersion {
		return h, fmt.Errorf("unsupported utxo dump version %d (only %d is supported)", v, utxoDumpVersion)
	}
	copy(h.Network[:], fixed[7:11])
	if h.Network != wantNetwork {
		return h, fmt.Errorf("utxo dump is for network magic %x, expected %x", h.Network, wantNetwork)
	}
	copy(h.BaseHash[:], fixed[11:43])
	h.CoinsCount = binary.LittleEndian.Uint64(fixed[43:51])
	return h, nil
}

// parseUtxoDump streams a dumptxoutset v2 file and rejects it if the coin count disagrees with the header.
func parseUtxoDump(r io.Reader, wantNetwork [4]byte, fn func(utxoRecord) error) (utxoHeader, error) {
	u := &utxoReader{r: bufio.NewReaderSize(r, 1<<16)}
	h, err := readUtxoHeader(u, wantNetwork)
	if err != nil {
		return h, err
	}
	var (
		txid [32]byte
		left uint64
	)
	for i := uint64(0); i < h.CoinsCount; i++ {
		rec, err := func() (utxoRecord, error) {
			if left == 0 {
				if err := u.full(txid[:]); err != nil {
					return utxoRecord{}, err
				}
				var err error
				if left, err = u.compactSize(); err != nil {
					return utxoRecord{}, err
				}
				if left == 0 {
					return utxoRecord{}, errors.New("txid group with zero coins")
				}
			}
			vout, err := u.compactSize()
			if err != nil {
				return utxoRecord{}, err
			}
			code, err := u.varint()
			if err != nil {
				return utxoRecord{}, err
			}
			if code>>1 > 1<<32-1 {
				return utxoRecord{}, fmt.Errorf("height %d out of range", code>>1)
			}
			comp, err := u.varint()
			if err != nil {
				return utxoRecord{}, err
			}
			script, err := u.script()
			if err != nil {
				return utxoRecord{}, err
			}
			left--
			return utxoRecord{
				ScriptPubKey: script,
				Txid:         txid,
				Vout:         vout,
				Amount:       decompressUtxoAmount(comp),
				Height:       uint32(code >> 1),
				Coinbase:     code&1 == 1,
			}, nil
		}()
		if err != nil {
			return h, fmt.Errorf("coin %d of %d: %w", i+1, h.CoinsCount, err)
		}
		if err := fn(rec); err != nil {
			return h, err
		}
	}
	if left != 0 {
		return h, fmt.Errorf("header says %d coins but the last txid group has %d more", h.CoinsCount, left)
	}
	if _, err := u.r.ReadByte(); err != io.EOF {
		return h, fmt.Errorf("utxo dump has more data than the %d coins in its header", h.CoinsCount)
	}
	return h, nil
}
