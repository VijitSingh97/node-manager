package main

import (
	"reflect"
	"testing"
)

func TestChainTableDrivesNamesLabelsAndPriceAssets(t *testing.T) {
	names := map[string]string{nodeBitcoin: "Bitcoin", nodeMonero: "Monero", nodeTari: "Tari", nodeLitecoin: "Litecoin", nodeDogecoin: "Dogecoin", nodeBitcoinCash: "Bitcoin Cash", nodeZcash: "Zcash", "other": "other"}
	for id, want := range names {
		if got := nodeDisplayName(id); got != want {
			t.Errorf("nodeDisplayName(%q) = %q, want %q", id, got, want)
		}
	}
	labels := map[string]string{"bitcoin_rpc": "Bitcoin RPC", "monero_rpc": "Monero RPC", "monero_zmq": "Monero ZMQ", "tari_grpc": "Tari gRPC", "litecoin_rpc": "Litecoin RPC", "dogecoin_rpc": "Dogecoin RPC", "bitcoincash_rpc": "Bitcoin Cash RPC", "zcash_rpc": "Zcash RPC", "tari_rpc": "", "bitcoin_zmq": "", "": ""}
	for id, want := range labels {
		if got := portControlLabel(id); got != want {
			t.Errorf("portControlLabel(%q) = %q, want %q", id, got, want)
		}
	}
	if want := []string{"bitcoin", "monero", "tari", "litecoin", "dogecoin", "bitcoin-cash", "zcash"}; !reflect.DeepEqual(priceAssets, want) {
		t.Errorf("priceAssets = %v", priceAssets)
	}
	cfg := &config{}
	t.Setenv("LITECOIN_ENABLED", "false")
	if err := loadAdditionalNodes(cfg); err != nil {
		t.Fatal(err)
	}
	for id, node := range cfg.nodes {
		if node.Name != names[id] {
			t.Errorf("node %q Name = %q", id, node.Name)
		}
	}
	found := false
	for _, port := range (&app{cfg: config{}}).portControlPorts() {
		if port.ID == "bitcoin_rpc" {
			found = true
			if port.Label != "Bitcoin RPC" {
				t.Errorf("bitcoin_rpc port label = %q", port.Label)
			}
		}
	}
	if !found {
		t.Error("bitcoin_rpc port missing")
	}
}
