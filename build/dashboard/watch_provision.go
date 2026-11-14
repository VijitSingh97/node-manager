package main

import (
	"encoding/json"
	"strings"
	"time"
)

func descriptorIdentity(item descriptor) string {
	return strings.SplitN(item.Desc, "#", 2)[0]
}

func descriptorTimestamp(value any) (int64, bool) {
	switch timestamp := value.(type) {
	case float64:
		return int64(timestamp), true
	case int:
		return int64(timestamp), true
	case int64:
		return timestamp, true
	case json.Number:
		result, err := timestamp.Int64()
		return result, err == nil
	default:
		return 0, false
	}
}

func descriptorRangeCovers(value any) bool {
	values, ok := value.([]any)
	if !ok || len(values) != 2 {
		return false
	}
	start, startOK := balanceValue(values[0])
	end, endOK := balanceValue(values[1])
	return startOK && endOK && start <= deriveRangeStart && end >= deriveRangeEnd
}

func walletMatches(walletRPC WalletRPCFunc, name string, entry WatchEntry) bool {
	descriptors, err := descriptorsFor(entry.Key)
	if err != nil {
		return false
	}
	type expectation struct {
		internal bool
		ranged   bool
	}
	want := make(map[string]expectation, len(descriptors))
	for _, item := range descriptors {
		info := mapValue(walletRPC(name, "getdescriptorinfo", []any{item.Desc}, 8*time.Second))
		canonical := stringValue(info["descriptor"])
		hasPrivateKeys, public := info["hasprivatekeys"].(bool)
		if canonical == "" || !public || hasPrivateKeys {
			return false
		}
		want[descriptorIdentity(descriptor{Desc: canonical})] = expectation{item.Internal, strings.Contains(canonical, "/*")}
	}
	listed := mapValue(walletRPC(name, "listdescriptors", []any{false}, 8*time.Second))
	values, ok := listed["descriptors"].([]any)
	if !ok || len(values) != len(want) {
		return false
	}
	seen := make(map[string]bool, len(values))
	requested := birthdayTimestamp(entry.Birthday)
	for _, value := range values {
		item := mapValue(value)
		identity := descriptorIdentity(descriptor{Desc: stringValue(item["desc"])})
		expectedDescriptor, expected := want[identity]
		if !expected || seen[identity] {
			return false
		}
		if internal, present := item["internal"]; present {
			role, valid := internal.(bool)
			if !valid || role != expectedDescriptor.internal {
				return false
			}
		}
		if expectedDescriptor.ranged && !descriptorRangeCovers(item["range"]) {
			return false
		}
		timestamp, present := item["timestamp"]
		if !present {
			return false
		}
		existing, valid := descriptorTimestamp(timestamp)
		if !valid {
			return false
		}
		if existing > requested && !(requested == 0 && existing <= 1) {
			return false
		}
		seen[identity] = true
	}
	return len(seen) == len(want)
}

func resolveWallet(walletRPC WalletRPCFunc, entry WatchEntry) (string, map[string]any) {
	for _, name := range []string{walletName(entry.Name), coreWalletName(entry)} {
		info := mapValue(walletRPC(name, "getwalletinfo", []any{}, 8*time.Second))
		if safeWatchWallet(info) && walletMatches(walletRPC, name, entry) {
			return name, info
		}
	}
	return "", nil
}

func safeWatchWallet(info map[string]any) bool {
	descriptors, descriptorsOK := info["descriptors"].(bool)
	privateKeys, privateKeysOK := info["private_keys_enabled"].(bool)
	return descriptorsOK && descriptors && privateKeysOK && !privateKeys
}

func probeCandidate(rpc RPCFunc, walletRPC WalletRPCFunc, name string) (bool, bool, bool) {
	if info := mapValue(walletRPC(name, "getwalletinfo", []any{}, 8*time.Second)); info != nil {
		return true, false, safeWatchWallet(info)
	}
	if rpc("loadwallet", []any{name}) == nil {
		return false, false, false
	}
	info := mapValue(walletRPC(name, "getwalletinfo", []any{}, 8*time.Second))
	return true, true, safeWatchWallet(info)
}

func importRequests(rpc RPCFunc, entry WatchEntry) ([]any, bool) {
	descriptors, err := descriptorsFor(entry.Key)
	if err != nil {
		return nil, false
	}
	requests := make([]any, 0, len(descriptors))
	for _, item := range descriptors {
		info := mapValue(rpc("getdescriptorinfo", []any{item.Desc}))
		hasPrivateKeys, public := info["hasprivatekeys"].(bool)
		if len(info) == 0 || !public || hasPrivateKeys || stringValue(info["descriptor"]) == "" {
			return nil, false
		}
		request := map[string]any{
			"desc": info["descriptor"], "timestamp": birthdayTimestamp(entry.Birthday),
			"active": false, "internal": item.Internal,
		}
		if truthy(info["isrange"]) {
			request["range"] = 1000
		}
		requests = append(requests, request)
	}
	return requests, len(requests) == len(descriptors)
}

func retryImportRequests(walletRPC WalletRPCFunc, name string, requests []any) ([]any, bool) {
	want := make(map[string]map[string]any, len(requests))
	for _, value := range requests {
		request := mapValue(value)
		identity := descriptorIdentity(descriptor{Desc: stringValue(request["desc"])})
		if identity == "" || want[identity] != nil {
			return nil, false
		}
		want[identity] = request
	}
	listed := mapValue(walletRPC(name, "listdescriptors", []any{false}, 8*time.Second))
	values, ok := listed["descriptors"].([]any)
	if !ok || len(values) > len(want) {
		return nil, false
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		item := mapValue(value)
		identity := descriptorIdentity(descriptor{Desc: stringValue(item["desc"])})
		request := want[identity]
		if request == nil || seen[identity] {
			return nil, false
		}
		expectedInternal, expectedInternalOK := request["internal"].(bool)
		if !expectedInternalOK {
			return nil, false
		}
		if internal, present := item["internal"]; present {
			role, valid := internal.(bool)
			if !valid || role != expectedInternal {
				return nil, false
			}
		}
		_, ranged := request["range"]
		if ranged && !descriptorRangeCovers(item["range"]) {
			return nil, false
		}
		existing, valid := descriptorTimestamp(item["timestamp"])
		requested, requestedOK := descriptorTimestamp(request["timestamp"])
		if !valid || !requestedOK || existing > requested && !(requested == 0 && existing <= 1) {
			return nil, false
		}
		seen[identity] = true
	}
	missing := make([]any, 0, len(requests)-len(seen))
	for _, value := range requests {
		identity := descriptorIdentity(descriptor{Desc: stringValue(mapValue(value)["desc"])})
		if !seen[identity] {
			missing = append(missing, value)
		}
	}
	return missing, true
}

func importDescriptors(walletRPC WalletRPCFunc, name string, requests []any) bool {
	results, ok := walletRPC(name, "importdescriptors", []any{requests}, time.Hour).([]any)
	if !ok || len(results) != len(requests) {
		return false
	}
	for _, result := range results {
		if !truthy(valueFrom(mapValue(result), "success")) {
			return false
		}
	}
	return true
}

func ProvisionOne(rpc RPCFunc, walletRPC WalletRPCFunc, entry WatchEntry, pruned bool) (string, bool) {
	legacy := walletName(entry.Name)
	legacyExists, legacyLoaded, legacySafe := probeCandidate(rpc, walletRPC, legacy)
	if legacyExists {
		if legacySafe && walletMatches(walletRPC, legacy, entry) {
			return legacy, true
		}
		if legacyLoaded {
			rpc("unloadwallet", []any{legacy, false})
		}
	}
	name := coreWalletName(entry)
	hashExists, hashLoaded, hashSafe := probeCandidate(rpc, walletRPC, name)
	if hashExists && hashSafe && walletMatches(walletRPC, name, entry) {
		return name, true
	}
	if pruned {
		if hashLoaded {
			rpc("unloadwallet", []any{name, false})
		}
		return "", false
	}
	requests, valid := importRequests(rpc, entry)
	if !valid {
		if hashLoaded {
			rpc("unloadwallet", []any{name, false})
		}
		return "", false
	}
	if hashExists {
		missing, compatible := retryImportRequests(walletRPC, name, requests)
		if hashSafe && compatible && len(missing) > 0 && importDescriptors(walletRPC, name, missing) {
			return name, true
		}
		if hashLoaded {
			rpc("unloadwallet", []any{name, false})
		}
		return "", false
	}
	if rpc("createwallet", []any{name, true, true, "", false, true, true}) == nil {
		return "", false
	}
	if !importDescriptors(walletRPC, name, requests) {
		return "", false
	}
	return name, true
}

func (s *WatchStore) DeprovisionNameIfUnused(rpc RPCFunc, walletRPC WalletRPCFunc, entry WatchEntry, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	identity := watchKeyID(entry.Key)
	for _, current := range s.entries {
		if watchKeyID(current.Key) == identity {
			return
		}
	}
	if name != walletName(entry.Name) && name != coreWalletName(entry) {
		return
	}
	info := mapValue(walletRPC(name, "getwalletinfo", []any{}, 8*time.Second))
	if safeWatchWallet(info) && walletMatches(walletRPC, name, entry) {
		rpc("unloadwallet", []any{name, false})
	}
}
