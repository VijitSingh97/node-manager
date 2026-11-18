package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type proofAuth struct {
	user string
	pass string
}

func (a proofAuth) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	encoded := base64.StdEncoding.EncodeToString([]byte(a.user + ":" + a.pass))
	return map[string]string{"authorization": "Basic " + encoded}, nil
}

func (proofAuth) RequireTransportSecurity() bool { return false }

type probeResult struct {
	fatal  bool
	reason string
}

func authRejected(err error) bool {
	code := status.Code(err)
	return code == codes.Unauthenticated || code == codes.PermissionDenied
}

func probe(endpoint, user, pass string) *probeResult {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	plain, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return &probeResult{fatal: true, reason: fmt.Sprintf("plain client: %v", err)}
	}
	_, unauthErr := NewWalletClient(plain).GetVersion(ctx, &TariGetVersionRequest{})
	plain.Close()
	switch status.Code(unauthErr) {
	case codes.Unauthenticated:
	case codes.OK:
		return &probeResult{fatal: true, reason: "GetVersion without credentials succeeded"}
	case codes.Unavailable, codes.DeadlineExceeded:
		return &probeResult{reason: fmt.Sprintf("GetVersion without credentials: %v", unauthErr)}
	default:
		return &probeResult{fatal: true, reason: fmt.Sprintf("GetVersion without credentials: want Unauthenticated, got %v", unauthErr)}
	}
	authed, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithPerRPCCredentials(proofAuth{user: user, pass: pass}))
	if err != nil {
		return &probeResult{fatal: true, reason: fmt.Sprintf("authenticated client: %v", err)}
	}
	defer authed.Close()
	client := NewWalletClient(authed)
	version, err := client.GetVersion(ctx, &TariGetVersionRequest{})
	if err == nil && strings.TrimSpace(version.GetVersion()) == "" {
		return &probeResult{fatal: true, reason: "GetVersion returned an empty version"}
	}
	if err != nil {
		return &probeResult{fatal: authRejected(err), reason: fmt.Sprintf("GetVersion: %v", err)}
	}
	address, err := client.GetCompleteAddress(ctx, &TariWalletEmpty{})
	if err != nil {
		return &probeResult{fatal: authRejected(err), reason: fmt.Sprintf("GetCompleteAddress: %v", err)}
	}
	if address.GetInteractiveAddressBase58() == "" && address.GetOneSidedAddressBase58() == "" {
		return &probeResult{fatal: true, reason: "GetCompleteAddress returned no address"}
	}
	if _, err := client.GetBalance(ctx, &TariGetBalanceRequest{}); err != nil {
		return &probeResult{fatal: authRejected(err), reason: fmt.Sprintf("GetBalance: %v", err)}
	}
	if _, err := client.GetState(ctx, &TariGetStateRequest{}); err != nil {
		return &probeResult{fatal: authRejected(err), reason: fmt.Sprintf("GetState: %v", err)}
	}
	return nil
}

func main() {
	endpoint := os.Getenv("TARI_PROOF_ENDPOINT")
	user := os.Getenv("TARI_PROOF_USER")
	pass := os.Getenv("TARI_PROOF_PASSWORD")
	if endpoint == "" || user == "" || pass == "" {
		fmt.Fprintln(os.Stderr, "FAIL: TARI_PROOF_ENDPOINT, TARI_PROOF_USER and TARI_PROOF_PASSWORD are required")
		os.Exit(2)
	}
	// The launcher reports ready once the gRPC port accepts connections, which can be before the wallet answers calls.
	deadline := time.Now().Add(90 * time.Second)
	for {
		result := probe(endpoint, user, pass)
		if result == nil {
			fmt.Println("PASS: Tari wallet gRPC rejects unauthenticated calls and accepts authenticated dashboard methods")
			return
		}
		if result.fatal || time.Now().After(deadline) {
			fmt.Fprintln(os.Stderr, "FAIL: Tari wallet gRPC probe: "+result.reason)
			os.Exit(1)
		}
		time.Sleep(time.Second)
	}
}
