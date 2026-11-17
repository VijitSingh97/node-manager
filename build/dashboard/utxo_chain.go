package main

import (
	"io"
	"strings"
)

type utxoChainParams struct {
	Networks       map[string][4]byte
	LegacyDump     bool
	DumpLatest     bool
	BlockVerbosity int
	WaitTip        bool
	Descriptors    []string
	ReverseIndex   bool
	Replay         bool
}

var (
	utxoBitcoin = &utxoChainParams{
		Networks:       map[string][4]byte{"main": utxoNetMainnet, "regtest": utxoNetRegtest},
		DumpLatest:     true,
		BlockVerbosity: 3,
		WaitTip:        true,
		Descriptors:    []string{"addr(", "pkh(", "wpkh(", "sh(wpkh(", "wsh(", "tr("},
	}
	// Litecoin 0.21.5.8, measured on regtest in the #238 P0 spike.
	utxoLitecoin = &utxoChainParams{
		Networks:       map[string][4]byte{"main": {0xfb, 0xc0, 0xb6, 0xdb}, "regtest": utxoNetRegtest},
		LegacyDump:     true,
		BlockVerbosity: 2,
		Descriptors:    []string{"addr(", "pkh(", "wpkh(", "sh(wpkh(", "wsh("},
		ReverseIndex:   true,
	}
	// Bitcoin Cash Node 29.2.0 has no dumptxoutset, so the index replays raw blocks; verbose JSON costs the node gigabytes on a 32 MB block.
	utxoBitcoinCash = &utxoChainParams{
		Networks:       map[string][4]byte{"main": {0xe3, 0xe1, 0xf3, 0xe8}, "regtest": {0xda, 0xb5, 0xbf, 0xfa}},
		BlockVerbosity: 0,
		ReverseIndex:   true,
		Replay:         true,
	}
)

// utxoChains lists the chains besides Bitcoin that can keep a balance index, by node id.
var utxoChains = map[string]*utxoChainParams{nodeLitecoin: utxoLitecoin, nodeBitcoinCash: utxoBitcoinCash}

func (c *utxoChainParams) dumpArgs(path string) []any {
	if c.DumpLatest {
		return []any{path, "latest"}
	}
	return []any{path}
}

func (c *utxoChainParams) waitArgs(tip string) []any {
	params := []any{utxoBlockWait.Milliseconds()}
	if c.WaitTip && tip != "" {
		params = append(params, tip)
	}
	return params
}

func (c *utxoChainParams) descriptorSupported(desc string) bool {
	desc = strings.TrimSpace(desc)
	for _, prefix := range c.Descriptors {
		if strings.HasPrefix(desc, prefix) {
			return true
		}
	}
	return false
}

func (c *utxoChainParams) parseDump(r io.Reader, network [4]byte, onHeader func(utxoHeader), fn func(utxoRecord) error) (utxoHeader, error) {
	if c.LegacyDump {
		return parseLegacyUtxoDump(r, onHeader, fn)
	}
	return parseUtxoDumpHeader(r, network, onHeader, fn)
}
