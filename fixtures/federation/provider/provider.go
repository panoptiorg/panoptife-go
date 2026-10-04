// Package provider mirrors the standard Go mockability idiom that FN-01
// (measurements 2026-09-01) found defeating gRPC client detection: a local,
// narrow `Client` interface declared over the generated pb client and injected
// with the real one. The interface is named exactly `Client` and lives outside
// any pb/api package — both conditions the name-convention detector refuses —
// so the boundary is recoverable only by method-set identity.
package provider

import (
	"context"

	"example.com/federation/pb"
)

// Client is the hand-rolled narrowing of pb.AccountClient.
type Client interface {
	GetAccount(ctx context.Context, in *pb.GetAccountRequest) (*pb.AccountResponse, error)
}

type Provider struct{ client Client }

// New injects the generated client — the only implementation that exists.
func New(c pb.AccountClient) *Provider { return &Provider{client: c} }

// Lookup carries the tainted number across the boundary through the local
// interface; the chain must still cross pb.Account/GetAccount.
func (p *Provider) Lookup(ctx context.Context, number string) (*pb.AccountResponse, error) {
	return p.client.GetAccount(ctx, &pb.GetAccountRequest{Number: number})
}
