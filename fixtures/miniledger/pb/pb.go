// Package pb mimics generated protobuf request messages: *Request types with
// nil-safe getters. Sourcing is structural (doc 19): the LedgerServer
// convention below marks handler request params as source_params — the
// catalog `Request\)\.Get*` regex is inert here since trivial getters are
// canonicalized to field projections (doc 20 §2.2.1).
package pb

import "context"

// LedgerServer is the generated gRPC server interface.
type LedgerServer interface {
	HandleRawQuery(context.Context, *RawQueryRequest) error
	HandleGetAccount(context.Context, *GetAccountRequest) (string, error)
	HandleReserves(context.Context, *CalcReservesRequest) error
	HandleEndOfDay(context.Context, *RunEodRequest) error
	HandleRecalc(context.Context, *RecalcRequest) error
	HandleEvent(context.Context, *PushEventRequest) error
}

// UnimplementedLedgerServer is embedded by server implementations.
type UnimplementedLedgerServer struct{}

func (UnimplementedLedgerServer) HandleRawQuery(context.Context, *RawQueryRequest) error {
	return nil
}

func (UnimplementedLedgerServer) HandleGetAccount(context.Context, *GetAccountRequest) (string, error) {
	return "", nil
}

func (UnimplementedLedgerServer) HandleReserves(context.Context, *CalcReservesRequest) error {
	return nil
}

func (UnimplementedLedgerServer) HandleEndOfDay(context.Context, *RunEodRequest) error {
	return nil
}

func (UnimplementedLedgerServer) HandleRecalc(context.Context, *RecalcRequest) error {
	return nil
}

func (UnimplementedLedgerServer) HandleEvent(context.Context, *PushEventRequest) error {
	return nil
}

type GetAccountRequest struct{ Number string }

func (r *GetAccountRequest) GetNumber() string {
	if r == nil {
		return ""
	}
	return r.Number
}

type CalcReservesRequest struct{ Portfolio string }

func (r *CalcReservesRequest) GetPortfolio() string {
	if r == nil {
		return ""
	}
	return r.Portfolio
}

type RunEodRequest struct{ Date string }

func (r *RunEodRequest) GetDate() string {
	if r == nil {
		return ""
	}
	return r.Date
}

type PushEventRequest struct{ Payload string }

func (r *PushEventRequest) GetPayload() string {
	if r == nil {
		return ""
	}
	return r.Payload
}

type RawQueryRequest struct{ Sql string }

func (r *RawQueryRequest) GetSql() string {
	if r == nil {
		return ""
	}
	return r.Sql
}

type RecalcRequest struct{ PortfolioId string }

func (r *RecalcRequest) GetPortfolioId() string {
	if r == nil {
		return ""
	}
	return r.PortfolioId
}
