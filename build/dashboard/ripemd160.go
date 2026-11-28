package main

import (
	"crypto/sha256"
	"encoding/binary"
	"math/bits"
)

var (
	ripemdRL = [80]uint8{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
		7, 4, 13, 1, 10, 6, 15, 3, 12, 0, 9, 5, 2, 14, 11, 8,
		3, 10, 14, 4, 9, 15, 8, 1, 2, 7, 0, 6, 13, 11, 5, 12,
		1, 9, 11, 10, 0, 8, 12, 4, 13, 3, 7, 15, 14, 5, 6, 2,
		4, 0, 5, 9, 7, 12, 2, 10, 14, 1, 3, 8, 11, 6, 15, 13}
	ripemdRR = [80]uint8{
		5, 14, 7, 0, 9, 2, 11, 4, 13, 6, 15, 8, 1, 10, 3, 12,
		6, 11, 3, 7, 0, 13, 5, 10, 14, 15, 8, 12, 4, 9, 1, 2,
		15, 5, 1, 3, 7, 14, 6, 9, 11, 8, 12, 2, 10, 0, 4, 13,
		8, 6, 4, 1, 3, 11, 15, 0, 5, 12, 2, 13, 9, 7, 10, 14,
		12, 15, 10, 4, 1, 5, 8, 7, 6, 2, 13, 14, 0, 3, 9, 11}
	ripemdSL = [80]uint8{
		11, 14, 15, 12, 5, 8, 7, 9, 11, 13, 14, 15, 6, 7, 9, 8,
		7, 6, 8, 13, 11, 9, 7, 15, 7, 12, 15, 9, 11, 7, 13, 12,
		11, 13, 6, 7, 14, 9, 13, 15, 14, 8, 13, 6, 5, 12, 7, 5,
		11, 12, 14, 15, 14, 15, 9, 8, 9, 14, 5, 6, 8, 6, 5, 12,
		9, 15, 5, 11, 6, 8, 13, 12, 5, 12, 13, 14, 11, 8, 5, 6}
	ripemdSR = [80]uint8{
		8, 9, 9, 11, 13, 15, 15, 5, 7, 7, 8, 11, 14, 14, 12, 6,
		9, 13, 15, 7, 12, 8, 9, 11, 7, 7, 12, 7, 6, 15, 13, 11,
		9, 7, 15, 11, 8, 6, 6, 14, 12, 13, 5, 14, 13, 13, 7, 5,
		15, 5, 8, 11, 14, 14, 6, 14, 6, 9, 12, 9, 12, 5, 15, 8,
		8, 5, 12, 9, 12, 5, 14, 6, 8, 13, 6, 5, 15, 13, 11, 11}
	ripemdKL = [5]uint32{0, 0x5a827999, 0x6ed9eba1, 0x8f1bbcdc, 0xa953fd4e}
	ripemdKR = [5]uint32{0x50a28be6, 0x5c4dd124, 0x6d703ef3, 0x7a6d76e9, 0}
)

func ripemdF(round int, x, y, z uint32) uint32 {
	switch round {
	case 0:
		return x ^ y ^ z
	case 1:
		return x&y | ^x&z
	case 2:
		return (x | ^y) ^ z
	case 3:
		return x&z | y&^z
	}
	return x ^ (y | ^z)
}

// ripemd160Of32 hashes exactly 32 bytes, which fits one block; that is all hash160 needs.
func ripemd160Of32(in [32]byte) [20]byte {
	var blk [64]byte
	copy(blk[:], in[:])
	blk[32] = 0x80
	binary.LittleEndian.PutUint64(blk[56:], 256)
	var x [16]uint32
	for i := range x {
		x[i] = binary.LittleEndian.Uint32(blk[4*i:])
	}
	h := [5]uint32{0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476, 0xc3d2e1f0}
	al, bl, cl, dl, el := h[0], h[1], h[2], h[3], h[4]
	ar, br, cr, dr, er := h[0], h[1], h[2], h[3], h[4]
	for j := 0; j < 80; j++ {
		r := j / 16
		t := bits.RotateLeft32(al+ripemdF(r, bl, cl, dl)+x[ripemdRL[j]]+ripemdKL[r], int(ripemdSL[j])) + el
		al, el, dl, cl, bl = el, dl, bits.RotateLeft32(cl, 10), bl, t
		t = bits.RotateLeft32(ar+ripemdF(4-r, br, cr, dr)+x[ripemdRR[j]]+ripemdKR[r], int(ripemdSR[j])) + er
		ar, er, dr, cr, br = er, dr, bits.RotateLeft32(cr, 10), br, t
	}
	t := h[1] + cl + dr
	h[1], h[2], h[3], h[4], h[0] = h[2]+dl+er, h[3]+el+ar, h[4]+al+br, h[0]+bl+cr, t
	var out [20]byte
	for i, v := range h {
		binary.LittleEndian.PutUint32(out[4*i:], v)
	}
	return out
}

func utxoHash160(key []byte) [20]byte {
	return ripemd160Of32(sha256.Sum256(key))
}
