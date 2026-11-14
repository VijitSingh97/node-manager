package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

const (
	deriveRangeStart = 0
	deriveRangeEnd   = 999
	bech32Charset    = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
)

type walletScripts struct {
	Scripts  [][]byte
	Fallback bool
}

func bech32Polymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if top>>uint(i)&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

func decodeSegwitAddress(addr string) ([]byte, error) {
	if addr != strings.ToLower(addr) && addr != strings.ToUpper(addr) {
		return nil, errors.New("mixed-case address")
	}
	addr = strings.ToLower(addr)
	sep := strings.LastIndexByte(addr, '1')
	hrp := ""
	if sep > 0 {
		hrp = addr[:sep]
	}
	if hrp != "bc" && hrp != "tb" && hrp != "bcrt" || len(addr)-sep-1 < 7 || len(addr) > 90 {
		return nil, errors.New("not a segwit address")
	}
	values := make([]byte, 0, len(addr))
	for i := 0; i < len(hrp); i++ {
		values = append(values, hrp[i]>>5)
	}
	values = append(values, 0)
	for i := 0; i < len(hrp); i++ {
		values = append(values, hrp[i]&31)
	}
	for _, c := range addr[sep+1:] {
		d := strings.IndexRune(bech32Charset, c)
		if d < 0 {
			return nil, errors.New("invalid bech32 character")
		}
		values = append(values, byte(d))
	}
	polymod := bech32Polymod(values)
	data := values[len(hrp)*2+1 : len(values)-6]
	if len(data) == 0 || data[0] > 16 {
		return nil, errors.New("invalid witness version")
	}
	version := data[0]
	if version == 0 && polymod != 1 || version != 0 && polymod != 0x2bc830a3 {
		return nil, errors.New("bad bech32 checksum")
	}
	var program []byte
	var acc, bits uint32
	for _, v := range data[1:] {
		acc = acc<<5 | uint32(v)
		bits += 5
		if bits >= 8 {
			bits -= 8
			program = append(program, byte(acc>>bits&0xff))
		}
	}
	if bits >= 5 || acc<<(8-bits)&0xff != 0 {
		return nil, errors.New("invalid bech32 padding")
	}
	if len(program) < 2 || len(program) > 40 || version == 0 && len(program) != 20 && len(program) != 32 {
		return nil, errors.New("invalid witness program length")
	}
	op := byte(0)
	if version > 0 {
		op = 0x50 + version
	}
	return append([]byte{op, byte(len(program))}, program...), nil
}

func addressScript(addr string) ([]byte, error) {
	if script, err := decodeSegwitAddress(addr); err == nil {
		return script, nil
	}
	raw, err := base58DecodeCheck(addr)
	if err != nil {
		return nil, err
	}
	if len(raw) != 21 {
		return nil, errors.New("invalid address length")
	}
	switch raw[0] {
	case 0x00, 0x6f:
		return append(append([]byte{0x76, 0xa9, 20}, raw[1:]...), 0x88, 0xac), nil
	case 0x05, 0xc4:
		return append(append([]byte{0xa9, 20}, raw[1:]...), 0x87), nil
	}
	return nil, errors.New("unknown address version")
}

func sortedUniqueScripts(scripts [][]byte) [][]byte {
	sort.Slice(scripts, func(i, j int) bool { return bytes.Compare(scripts[i], scripts[j]) < 0 })
	out := scripts[:0]
	for i, s := range scripts {
		if i == 0 || !bytes.Equal(s, scripts[i-1]) {
			out = append(out, s)
		}
	}
	return out
}

const deriveBatchSize = 20

func descriptorSupported(desc string) bool {
	desc = strings.TrimSpace(desc)
	for _, prefix := range []string{"addr(", "pkh(", "wpkh(", "sh(wpkh(", "wsh(", "tr("} {
		if strings.HasPrefix(desc, prefix) {
			return true
		}
	}
	return false
}

func batchChunks(ctx context.Context, rpc *rpcClient, calls []rpcBatchCall) ([]rpcResponse, error) {
	var out []rpcResponse
	for start := 0; start < len(calls); start += deriveBatchSize {
		end := min(start+deriveBatchSize, len(calls))
		part, err := rpc.callBatch(ctx, calls[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
	}
	return out, nil
}

// One batched call per chunk of descriptors; a wallet that cannot be turned into a complete script set is marked Fallback for the scantxoutset path.
func resolveWalletScripts(ctx context.Context, rpc *rpcClient, keys []string) ([]walletScripts, error) {
	var infoCalls []rpcBatchCall
	var owners []int
	result := make([]walletScripts, len(keys))
	for w, key := range keys {
		descs, err := descriptorsFor(key)
		if err != nil {
			if hasPrivateMaterial(key) {
				return nil, err
			}
			result[w].Fallback = true
			continue
		}
		for _, d := range descs {
			if !descriptorSupported(d.Desc) {
				result[w].Fallback = true
			}
		}
		if result[w].Fallback {
			continue
		}
		for _, d := range descs {
			infoCalls = append(infoCalls, rpcBatchCall{"getdescriptorinfo", []any{d.Desc}})
			owners = append(owners, w)
		}
	}
	infos, err := batchChunks(ctx, rpc, infoCalls)
	if err != nil {
		return nil, err
	}
	var deriveCalls []rpcBatchCall
	var deriveOwners []int
	var deriveWant []int
	for i, r := range infos {
		w := owners[i]
		if result[w].Fallback {
			continue
		}
		var info struct {
			Descriptor     string `json:"descriptor"`
			IsRange        *bool  `json:"isrange"`
			HasPrivateKeys *bool  `json:"hasprivatekeys"`
		}
		if json.Unmarshal(r.Result, &info) != nil || info.Descriptor == "" || info.IsRange == nil || info.HasPrivateKeys == nil {
			result[w].Fallback = true
			continue
		}
		if *info.HasPrivateKeys {
			return nil, errors.New("private key material is not allowed")
		}
		params := []any{info.Descriptor}
		want := 1
		if *info.IsRange {
			params = append(params, []int{deriveRangeStart, deriveRangeEnd})
			want = deriveRangeEnd - deriveRangeStart + 1
		}
		deriveCalls = append(deriveCalls, rpcBatchCall{"deriveaddresses", params})
		deriveOwners = append(deriveOwners, w)
		deriveWant = append(deriveWant, want)
	}
	derived, err := batchChunks(ctx, rpc, deriveCalls)
	if err != nil {
		return nil, err
	}
	for i, r := range derived {
		w := deriveOwners[i]
		if result[w].Fallback {
			continue
		}
		var addresses []string
		if json.Unmarshal(r.Result, &addresses) != nil || len(addresses) != deriveWant[i] {
			result[w] = walletScripts{Fallback: true}
			continue
		}
		for _, a := range addresses {
			script, err := addressScript(a)
			if err != nil {
				result[w] = walletScripts{Fallback: true}
				break
			}
			result[w].Scripts = append(result[w].Scripts, script)
		}
	}
	for w := range result {
		result[w].Scripts = sortedUniqueScripts(result[w].Scripts)
	}
	return result, nil
}
