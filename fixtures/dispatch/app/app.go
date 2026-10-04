package app

import (
	"context"

	"example.com/dispatch/pb"
	"example.com/dispatch/store"
)

type App struct {
	pb.UnimplementedQueryServer
	repo   Repo
	codecs []Codec
	st     *store.Storage
}

// New instantiates BOTH Repo impls and ALL Codec impls: VTA is flow-based, so
// only allocated types enter the target sets (n=2 narrow, n=12 wide).
func New(usePg bool) *App {
	var r Repo
	if usePg {
		r = &PgRepo{st: &store.Storage{}}
	} else {
		r = &MemRepo{}
	}
	return &App{
		repo: r,
		codecs: []Codec{
			Codec01{}, Codec02{}, Codec03{}, Codec04{}, Codec05{}, Codec06{},
			Codec07{}, Codec08{}, Codec09{}, Codec10{}, Codec11{}, Codec12{},
		},
		st: &store.Storage{},
	}
}

// HandleNarrow: GetQuery (SOURCE) → Repo.Save (narrow dispatch) → Selectx
// (SINK inside PgRepo.Save). Chain exists only with dispatch wired.
func (a *App) HandleNarrow(ctx context.Context, req *pb.QueryRequest) error {
	_, err := a.repo.Save(req.GetQuery())
	return err
}

// HandleWide: GetQuery (SOURCE) → Codec.Encode (wide, capped → opaque,
// taint-transparent default leaf) → Selectx (local SINK). Chain must survive
// under every dispatch mode; the exec sink inside Codec07 must not.
func (a *App) HandleWide(ctx context.Context, req *pb.QueryRequest) error {
	q := req.GetQuery()
	for _, c := range a.codecs {
		q = c.Encode(q)
	}
	_, err := a.st.Selectx(q)
	return err
}
