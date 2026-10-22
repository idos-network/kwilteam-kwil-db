package txapp

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/trufnetwork/kwil-db/common"
	"github.com/trufnetwork/kwil-db/core/crypto"
	"github.com/trufnetwork/kwil-db/core/log"
	"github.com/trufnetwork/kwil-db/core/types"
	"github.com/trufnetwork/kwil-db/extensions/resolutions"
	"github.com/trufnetwork/kwil-db/node/types/sql"
)

const expiryGuardType = "expiry_guard"

var expiryGuardResolved []*types.UUID

func init() {
	err := resolutions.RegisterResolution(expiryGuardType, resolutions.ModAdd, resolutions.ResolutionConfig{
		ConfirmationThreshold: big.NewRat(1, 2),
		RefundThreshold:       big.NewRat(1, 2),
		ResolveFunc: func(_ context.Context, _ *common.App, resolution *resolutions.Resolution, _ *common.BlockContext) error {
			expiryGuardResolved = append(expiryGuardResolved, resolution.ID)
			return nil
		},
	})
	if err != nil {
		panic(err)
	}
}

func TestResolutionExpired(t *testing.T) {
	expiration := time.Unix(1_700_000_000, 0)
	require.False(t, resolutionExpired(expiration, expiration.Unix()-1))
	require.True(t, resolutionExpired(expiration, expiration.Unix()))
	require.True(t, resolutionExpired(expiration, expiration.Unix()+1))
}

func TestProcessVotesSkipsExpiredConfirmation(t *testing.T) {
	expiryGuardResolved = nil
	blockTime := int64(1_700_000_000)
	proposer := &types.Validator{
		AccountID: types.AccountID{
			Identifier: []byte{0xab},
			KeyType:    crypto.KeyTypeEd25519,
		},
		Power: 1,
	}
	body := []byte{0x01}
	expiredID := testUUID(1)
	liveID := testUUID(2)
	expiredRes := &resolutions.Resolution{
		ID:            expiredID,
		Body:          body,
		Type:          expiryGuardType,
		Expiration:    time.Unix(blockTime, 0),
		ApprovedPower: 1,
		Voters:        []*types.Validator{proposer},
		Proposer:      proposer,
	}
	liveRes := &resolutions.Resolution{
		ID:            liveID,
		Body:          body,
		Type:          expiryGuardType,
		Expiration:    time.Unix(blockTime+1, 0),
		ApprovedPower: 1,
		Voters:        []*types.Validator{proposer},
		Proposer:      proposer,
	}

	origThreshold := getResolutionsByThresholdAndType
	origExpired := getExpired
	origDelete := deleteResolutions
	origMark := markProcessed
	origEvents := deleteEvents
	origPower := requiredPower
	t.Cleanup(func() {
		getResolutionsByThresholdAndType = origThreshold
		getExpired = origExpired
		deleteResolutions = origDelete
		markProcessed = origMark
		deleteEvents = origEvents
		requiredPower = origPower
	})

	getResolutionsByThresholdAndType = func(context.Context, sql.TxMaker, *big.Rat, string, int64) ([]*resolutions.Resolution, error) {
		return []*resolutions.Resolution{expiredRes, liveRes}, nil
	}
	getExpired = func(context.Context, sql.Executor, int64) ([]*resolutions.Resolution, error) {
		return []*resolutions.Resolution{expiredRes}, nil
	}
	deleteResolutions = func(context.Context, sql.Executor, ...*types.UUID) error { return nil }
	var marked []*types.UUID
	markProcessed = func(_ context.Context, _ sql.Executor, ids ...*types.UUID) error {
		marked = append(marked, ids...)
		return nil
	}
	deleteEvents = func(context.Context, sql.DB, ...*types.UUID) error { return nil }
	requiredPower = func(context.Context, sql.Executor, *big.Rat, int64) int64 { return 1 }

	accts := &recordingAccounts{}
	app := &TxApp{
		Accounts:   accts,
		Validators: &mockValidator{getVoterFn: func() (int64, error) { return 1, nil }},
		service:    &common.Service{Logger: log.DiscardLogger},
		resTypes:   []string{expiryGuardType},
	}
	_, err := app.processVotes(context.Background(), &mockDb{}, &common.BlockContext{
		Timestamp: blockTime,
		ChainContext: &common.ChainContext{
			NetworkParameters: &types.NetworkParameters{},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []*types.UUID{liveID}, expiryGuardResolved)
	require.Equal(t, []*types.UUID{liveID, expiredID}, marked)
	require.Len(t, accts.amounts, 1)
	require.Equal(t, 2*ValidatorVoteBodyBytePrice*int64(len(body)), accts.amounts[0].Int64())
}

func testUUID(n byte) *types.UUID {
	id := types.UUID{n}
	return &id
}

type recordingAccounts struct {
	mockAccount
	amounts []*big.Int
}

func (a *recordingAccounts) Credit(_ context.Context, _ sql.Executor, _ *types.AccountID, amount *big.Int) error {
	a.amounts = append(a.amounts, new(big.Int).Set(amount))
	return nil
}
