package blockprocessor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	ktypes "github.com/trufnetwork/kwil-db/core/types"
)

func TestBlockExecutionProfileAggregatesActionsAndSlowTransactions(t *testing.T) {
	t.Parallel()

	executeTx := executionProfileTestActionTx(t, "idos", "finalize_credentials_as_gateway", 2)
	transferTx := &ktypes.Transaction{
		Body: &ktypes.TransactionBody{PayloadType: ktypes.PayloadTypeTransfer},
	}
	profile := newBlockExecutionProfile()
	profile.record(
		executeTx,
		ktypes.HashBytes([]byte("execute-1")),
		uint32(ktypes.CodeOk),
		300*time.Millisecond,
		250*time.Millisecond,
	)
	profile.record(
		executeTx,
		ktypes.HashBytes([]byte("execute-2")),
		1,
		700*time.Millisecond,
		600*time.Millisecond,
	)
	profile.record(
		transferTx,
		ktypes.HashBytes([]byte("transfer")),
		uint32(ktypes.CodeOk),
		2*time.Second,
		1500*time.Millisecond,
	)

	require.True(t, profile.shouldLog())
	require.Equal(t, 3*time.Second, profile.total)
	require.Equal(t, []actionExecutionProfile{
		{
			PayloadType:   "transfer",
			Transactions:  1,
			Calls:         1,
			TotalMs:       2_000,
			MaxMs:         2_000,
			PayloadMs:     1_500,
			MaxPayloadMs:  1_500,
			OverheadMs:    500,
			MaxOverheadMs: 500,
		},
		{
			PayloadType:   "execute",
			Namespace:     "idos",
			Action:        "finalize_credentials_as_gateway",
			Transactions:  2,
			Calls:         4,
			Failures:      1,
			TotalMs:       1_000,
			MaxMs:         700,
			PayloadMs:     850,
			MaxPayloadMs:  600,
			OverheadMs:    150,
			MaxOverheadMs: 100,
		},
	}, profile.actionProfiles())

	slowTxs := profile.slowTransactions()
	require.Len(t, slowTxs, 1)
	require.Equal(t, "transfer", slowTxs[0].PayloadType)
	require.Equal(t, int64(2_000), slowTxs[0].DurationMs)
	require.Equal(t, int64(1_500), slowTxs[0].PayloadMs)
	require.Equal(t, int64(500), slowTxs[0].OverheadMs)
}

func TestBlockExecutionProfileLogsCumulativeSlowBlock(t *testing.T) {
	t.Parallel()

	tx := executionProfileTestActionTx(t, "idos", "create_preliminary_credential", 1)
	profile := newBlockExecutionProfile()
	for i := range 10 {
		profile.record(
			tx,
			ktypes.HashBytes([]byte{byte(i)}),
			uint32(ktypes.CodeOk),
			500*time.Millisecond,
			400*time.Millisecond,
		)
	}

	require.True(t, profile.shouldLog())
	require.Empty(t, profile.slowTransactions())
	require.Equal(t, int64(5_000), profile.actionProfiles()[0].TotalMs)
	require.Equal(t, int64(4_000), profile.actionProfiles()[0].PayloadMs)
	require.Equal(t, int64(1_000), profile.actionProfiles()[0].OverheadMs)
}

func executionProfileTestActionTx(
	t *testing.T,
	namespace string,
	action string,
	calls int,
) *ktypes.Transaction {
	t.Helper()
	payload, err := (&ktypes.ActionExecution{
		Namespace: namespace,
		Action:    action,
		Arguments: make([][]*ktypes.EncodedValue, calls),
	}).MarshalBinary()
	require.NoError(t, err)
	return &ktypes.Transaction{
		Body: &ktypes.TransactionBody{
			PayloadType: ktypes.PayloadTypeExecute,
			Payload:     payload,
		},
	}
}
