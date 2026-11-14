package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUnavailableWalletBalancesAreExplicit(t *testing.T) {
	for _, id := range []string{nodeDogecoin, nodeZcash} {
		supported, reason := (&app{}).familyWatchCapability(id)
		if supported || reason == "" {
			t.Fatalf("%s capability = (%v, %q)", id, supported, reason)
		}
		a := &app{cfg: config{nodes: map[string]nodeConfig{id: {ID: id, Enabled: true}}}}
		request := httptest.NewRequest(http.MethodGet, "/api/"+id+"/watch", nil)
		request.SetPathValue("node", id)
		response := httptest.NewRecorder()
		a.apiFamilyWatchList(response, request)
		var body struct {
			Supported bool                    `json:"supported"`
			Wallets   []familyWatchWalletView `json:"wallets"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Supported || len(body.Wallets) != 0 {
			t.Fatalf("%s response = %d %s", id, response.Code, response.Body.String())
		}
	}
}
