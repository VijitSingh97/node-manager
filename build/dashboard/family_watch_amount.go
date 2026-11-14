package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var familyWatchSeeds = map[string]familyWatchEntry{
	nodeLitecoin:    familyWatchExample("Linux Mint", "LW1Zckzn9Y6baEvhoo2tGBJJa5YLXXD1B2", "https://www.linuxmint.com/donors.php"),
	nodeBitcoinCash: familyWatchExample("Bitcoin Cash Node", "bitcoincash:pq22wvjxu3et9rxgw8zgwcs0j3270gs6lumlq52mxl", "https://bitcoincashnode.org/en/"),
	nodeDogecoin:    familyWatchExample("Dogecoin Foundation", "D89DhnsgKncmN12RejxudfU8AwXp3946q1", "https://foundation.dogecoin.com/about/"),
	nodeZcash:       familyWatchExample("Zcash Community Grants", "t3cFfPt1Bcvgez9ZbMBFWeZsskxTkPzGCow", "https://zips.z.cash/zip-0214"),
}

func familyWatchExample(label, address, source string) familyWatchEntry {
	return familyWatchEntry{ID: familyWatchID(address), Label: label, Address: address, SourceURL: source, Example: true}
}

func defaultFamilyWatchEntry(id string) familyWatchEntry {
	return familyWatchSeeds[id]
}

func familyWatchID(address string) string {
	sum := sha256.Sum256([]byte(address))
	return hex.EncodeToString(sum[:12])
}

func validFamilyWatchID(id string) bool {
	return len(id) == 24 && strings.Trim(id, "0123456789abcdef") == ""
}

func validFamilyWatchLabel(label string) bool {
	if label == "" || len(label) > 80 || strings.TrimSpace(label) != label {
		return false
	}
	for _, r := range label {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validFamilyWatchAddress(address string) bool {
	return address != "" && len(address) <= 200 && strings.TrimSpace(address) == address
}

func parseFamilyWatchAmount(raw json.RawMessage, maxSupply uint64) (string, error) {
	value := string(bytes.TrimSpace(raw))
	if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "eE+\"") {
		return "", errors.New("invalid amount")
	}
	whole, fraction, found := strings.Cut(value, ".")
	if whole == "" || len(fraction) > 8 || found && fraction == "" {
		return "", errors.New("invalid amount")
	}
	if !decimalDigits(whole) || found && !decimalDigits(fraction) {
		return "", errors.New("invalid amount")
	}
	wholeValue, err := strconv.ParseUint(whole, 10, 64)
	if err != nil || wholeValue > maxSupply/100_000_000 {
		return "", errors.New("invalid amount")
	}
	fraction += strings.Repeat("0", 8-len(fraction))
	fractionValue := uint64(0)
	if fraction != "" {
		fractionValue, err = strconv.ParseUint(fraction, 10, 64)
	}
	atomic := wholeValue*100_000_000 + fractionValue
	if err != nil || atomic > maxSupply {
		return "", errors.New("invalid amount")
	}
	return strconv.FormatUint(atomic, 10), nil
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func formatFamilyWatchAmount(atomic string) string {
	value, err := strconv.ParseUint(atomic, 10, 64)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d.%08d", value/100_000_000, value%100_000_000)
}

func validFamilyWatchBalance(balance familyWatchBalance, maxSupply uint64) bool {
	atomic, err := strconv.ParseUint(balance.Amount, 10, 64)
	if err != nil || atomic > maxSupply || balance.ScanHeight < 0 || balance.ScanTime <= 0 || balance.BestBlock == "" || len(balance.History) > maxFamilyWatchHistory {
		return false
	}
	for _, point := range balance.History {
		if point.Time <= 0 {
			return false
		}
		parsed, err := parseFamilyWatchAmount(json.RawMessage(point.Amount), maxSupply)
		if err != nil || formatFamilyWatchAmount(parsed) != point.Amount {
			return false
		}
	}
	return true
}
