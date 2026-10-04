package graph

import (
	"context"

	"example.com/federation/pb"
	"example.com/federation/provider"
	"example.com/federation/store"
)

// Account is a gqlgen model; AccountNumber is a GraphQL @key field (SOURCE).
type Account struct{ AccountNumber string }

func (a *Account) GetAccountNumber() string { return a.AccountNumber }

type accountResolver struct {
	client pb.AccountClient
	feed   pb.FeedClient
	prov   *provider.Provider
}

// DetailsViaLocalClient (FN-01): the remote call is made through a
// hand-rolled `provider.Client` interface, not the pb type. RED under the
// name-convention detector alone; the boundary must still be detected.
func (r *accountResolver) DetailsViaLocalClient(ctx context.Context, obj *Account) (*pb.AccountResponse, error) {
	return r.prov.Lookup(ctx, obj.GetAccountNumber())
}

// Details resolves Account.details by calling the ledger backend over gRPC.
// obj.GetAccountNumber() (GraphQL input) -> GetAccountRequest.Number -> invokes_remote.
func (r *accountResolver) Details(ctx context.Context, obj *Account) (*pb.AccountResponse, error) {
	return r.client.GetAccount(ctx, &pb.GetAccountRequest{Number: obj.GetAccountNumber()})
}

// Archive (doc 17 Shape A): the tainted GraphQL input crosses TWO service
// boundaries — backend.Archive has no local sink and forwards the entry to the
// downstream ledger's SQL sink. The chain surfaces only after the cross-repo
// fixpoint (the backend handler is summarized before the ledger view exists).
func (r *accountResolver) Archive(ctx context.Context, obj *Account) (*pb.ArchiveResponse, error) {
	return r.client.Archive(ctx, &pb.ArchiveRequest{Entry: obj.GetAccountNumber()})
}

// DetailsViaHelper (doc 17 Shape B): the invokes_remote lives in a LOCAL
// helper. Pre-fixpoint the resolver composes the helper's stale default-leaf
// summary (no remote sink), so the chain is lost; post-fixpoint the helper's
// summary carries the backend sink.
func (r *accountResolver) DetailsViaHelper(ctx context.Context, obj *Account) (*pb.AccountResponse, error) {
	return r.fetchDetails(ctx, obj.GetAccountNumber())
}

func (r *accountResolver) fetchDetails(ctx context.Context, number string) (*pb.AccountResponse, error) {
	return r.client.GetAccount(ctx, &pb.GetAccountRequest{Number: number})
}

// StreamDetails: server-stream corollary (doc 16 §3.2). The tainted GraphQL
// input flows into the remote Download; the handler's response arrives via
// stream.Recv() (contract StreamOut), then hits a LOCAL SQL sink.
func (a *accountResolver) StreamDetails(ctx context.Context, obj *Account, st *store.Storage) (string, error) {
	ds, err := a.feed.Download(ctx, &pb.DownloadRequest{Number: obj.GetAccountNumber()})
	if err != nil {
		return "", err
	}
	m, err := ds.Recv()
	if err != nil {
		return "", err
	}
	if err := ds.CloseSend(); err != nil {
		return "", err
	}
	return st.Selectx(m.GetData())
}

// SendUpload: client-stream — the tainted GraphQL input leaves via stream.Send
// (contract StreamIn) and reaches the BACKEND's SQL sink.
func (a *accountResolver) SendUpload(ctx context.Context, obj *Account) error {
	us, err := a.feed.Upload(ctx)
	if err != nil {
		return err
	}
	if err := us.Send(&pb.UploadRequest{Query: obj.GetAccountNumber()}); err != nil {
		return err
	}
	_, err = us.CloseAndRecv()
	return err
}

// queryResolver implements generated.QueryResolver (doc 19 acceptance): the
// untrusted GraphQL arg is the BARE `token` param — no getter, so only
// endpoint-driven source_params seeding can root a chain here.
type queryResolver struct{ client pb.AccountClient }

func (r *queryResolver) SearchByToken(ctx context.Context, token string) (string, error) {
	resp, err := r.client.GetAccount(ctx, &pb.GetAccountRequest{Number: token})
	if err != nil {
		return "", err
	}
	return resp.Account, nil
}

// generated-style dispatcher (mirrors gqlgen's ExecutableSchema calling resolvers).
type Resolver struct {
	Account pb.AccountClient
	Feed    pb.FeedClient
	Store   *store.Storage
	Prov    *provider.Provider
}

// NewResolver wires the provider the way real DI does: the generated client
// is converted to the narrow local interface at construction.
func NewResolver(acc pb.AccountClient, feed pb.FeedClient, st *store.Storage) *Resolver {
	return &Resolver{Account: acc, Feed: feed, Store: st, Prov: provider.New(acc)}
}

// Query wires the root resolver the way gqlgen's Resolver root does (the
// interface conversion is what real gqlgen wiring performs; it also makes
// queryResolver a runtime type so SSA sees its methods). The anonymous
// interface avoids importing generated (which imports this package for the
// model types — real gqlgen splits models into their own package instead).
func (r *Resolver) Query() interface {
	SearchByToken(ctx context.Context, token string) (string, error)
} {
	return &queryResolver{client: r.Account}
}

func (r *Resolver) resolveAccountDetails(ctx context.Context, obj *Account) (*pb.AccountResponse, error) {
	ar := &accountResolver{client: r.Account, prov: r.Prov}
	if _, err := ar.DetailsViaLocalClient(ctx, obj); err != nil {
		return nil, err
	}
	return ar.Details(ctx, obj)
}

func (r *Resolver) resolveAccountArchive(ctx context.Context, obj *Account) error {
	ar := &accountResolver{client: r.Account}
	if _, err := ar.Archive(ctx, obj); err != nil {
		return err
	}
	_, err := ar.DetailsViaHelper(ctx, obj)
	return err
}

func (r *Resolver) resolveAccountStreams(ctx context.Context, obj *Account) (string, error) {
	ar := &accountResolver{client: r.Account, feed: r.Feed}
	if err := ar.SendUpload(ctx, obj); err != nil {
		return "", err
	}
	return ar.StreamDetails(ctx, obj, r.Store)
}

// Exec is the generated entrypoint (keeps resolvers reachable).
func (r *Resolver) Exec(ctx context.Context, obj *Account) (*pb.AccountResponse, error) {
	if _, err := r.resolveAccountStreams(ctx, obj); err != nil {
		return nil, err
	}
	if err := r.resolveAccountArchive(ctx, obj); err != nil {
		return nil, err
	}
	return r.resolveAccountDetails(ctx, obj)
}
