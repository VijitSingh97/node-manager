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
	}
)

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
