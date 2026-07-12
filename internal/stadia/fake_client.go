package stadia

import (
	"context"
	"sync"
)

// FakeClient is an in-memory Stadia adapter for use in tests only.
type FakeClient struct {
	mu sync.Mutex

	IsochroneResp *IsochroneResponse
	IsochroneErr  error
	MatrixResp    *MatrixResponse
	MatrixErr     error

	IsochoneCalls []IsochroneRequest
	MatrixCalls   []MatrixRequest
}

func (f *FakeClient) Isochrone(_ context.Context, req IsochroneRequest) (*IsochroneResponse, error) {
	f.mu.Lock()
	f.IsochoneCalls = append(f.IsochoneCalls, req)
	f.mu.Unlock()
	return f.IsochroneResp, f.IsochroneErr
}

func (f *FakeClient) Matrix(_ context.Context, req MatrixRequest) (*MatrixResponse, error) {
	f.mu.Lock()
	f.MatrixCalls = append(f.MatrixCalls, req)
	f.mu.Unlock()
	return f.MatrixResp, f.MatrixErr
}
