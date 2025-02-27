// Code generated by mockery v2.5.1. DO NOT EDIT.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	types "github.com/klyed/tendermint/abci/types"
)

// AppConnSnapshot is an autogenerated mock type for the AppConnSnapshot type
type AppConnSnapshot struct {
	mock.Mock
}

// ApplySnapshotChunkSync provides a mock function with given fields: _a0, _a1
func (_m *AppConnSnapshot) ApplySnapshotChunkSync(_a0 context.Context, _a1 types.RequestApplySnapshotChunk) (*types.ResponseApplySnapshotChunk, error) {
	ret := _m.Called(_a0, _a1)

	var r0 *types.ResponseApplySnapshotChunk
	if rf, ok := ret.Get(0).(func(context.Context, types.RequestApplySnapshotChunk) *types.ResponseApplySnapshotChunk); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.ResponseApplySnapshotChunk)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, types.RequestApplySnapshotChunk) error); ok {
		r1 = rf(_a0, _a1)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Error provides a mock function with given fields:
func (_m *AppConnSnapshot) Error() error {
	ret := _m.Called()

	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// ListSnapshotsSync provides a mock function with given fields: _a0, _a1
func (_m *AppConnSnapshot) ListSnapshotsSync(_a0 context.Context, _a1 types.RequestListSnapshots) (*types.ResponseListSnapshots, error) {
	ret := _m.Called(_a0, _a1)

	var r0 *types.ResponseListSnapshots
	if rf, ok := ret.Get(0).(func(context.Context, types.RequestListSnapshots) *types.ResponseListSnapshots); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.ResponseListSnapshots)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, types.RequestListSnapshots) error); ok {
		r1 = rf(_a0, _a1)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// LoadSnapshotChunkSync provides a mock function with given fields: _a0, _a1
func (_m *AppConnSnapshot) LoadSnapshotChunkSync(_a0 context.Context, _a1 types.RequestLoadSnapshotChunk) (*types.ResponseLoadSnapshotChunk, error) {
	ret := _m.Called(_a0, _a1)

	var r0 *types.ResponseLoadSnapshotChunk
	if rf, ok := ret.Get(0).(func(context.Context, types.RequestLoadSnapshotChunk) *types.ResponseLoadSnapshotChunk); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.ResponseLoadSnapshotChunk)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, types.RequestLoadSnapshotChunk) error); ok {
		r1 = rf(_a0, _a1)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// OfferSnapshotSync provides a mock function with given fields: _a0, _a1
func (_m *AppConnSnapshot) OfferSnapshotSync(_a0 context.Context, _a1 types.RequestOfferSnapshot) (*types.ResponseOfferSnapshot, error) {
	ret := _m.Called(_a0, _a1)

	var r0 *types.ResponseOfferSnapshot
	if rf, ok := ret.Get(0).(func(context.Context, types.RequestOfferSnapshot) *types.ResponseOfferSnapshot); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.ResponseOfferSnapshot)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, types.RequestOfferSnapshot) error); ok {
		r1 = rf(_a0, _a1)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}
