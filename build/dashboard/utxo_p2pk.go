package main

const utxoP2PKBit = 1 << 31

// utxoP2PKKeyHash matches Core's pay-to-pubkey template: a direct push of a 33 or 65 byte key, then OP_CHECKSIG.
func utxoP2PKKeyHash(script []byte) ([20]byte, bool) {
	n := len(script)
	if n < 2 || script[n-1] != 0xac {
		return [20]byte{}, false
	}
	key := script[1 : n-1]
	switch {
	case n == 35 && script[0] == 33 && (key[0] == 0x02 || key[0] == 0x03):
	case n == 67 && script[0] == 65 && (key[0] == 0x04 || key[0] == 0x06 || key[0] == 0x07):
	default:
		return [20]byte{}, false
	}
	return utxoHash160(key), true
}

// utxoCanonicalScript maps a P2PK script to the P2PKH script of the same key, so both share one index prefix.
func utxoCanonicalScript(script []byte, buf *[25]byte) ([]byte, bool) {
	h, ok := utxoP2PKKeyHash(script)
	if !ok {
		return script, false
	}
	buf[0], buf[1], buf[2] = 0x76, 0xa9, 0x14
	copy(buf[3:], h[:])
	buf[23], buf[24] = 0x88, 0xac
	return buf[:], true
}

func utxoCanonHash(script []byte) ([utxoPrefixLen]byte, bool) {
	var buf [25]byte
	c, p2pk := utxoCanonicalScript(script, &buf)
	return utxoScriptHash(c), p2pk
}
