package blockprocessor

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/trufnetwork/kwil-db/common"
	"github.com/trufnetwork/kwil-db/core/crypto"
	"github.com/trufnetwork/kwil-db/core/crypto/auth"
	"github.com/trufnetwork/kwil-db/core/log"
	ktypes "github.com/trufnetwork/kwil-db/core/types"
	authExt "github.com/trufnetwork/kwil-db/extensions/auth"
	"github.com/trufnetwork/kwil-db/node/migrations"
	"github.com/trufnetwork/kwil-db/node/txapp"
	nodetypes "github.com/trufnetwork/kwil-db/node/types"
	"github.com/trufnetwork/kwil-db/node/types/sql"
)

// expiryAuthName is a context-bound authenticator whose signature data is a
// unix expiry time, checked against the block timestamp like mm_token.
const expiryAuthName = "test_expiry_auth"

type expiryAuthenticator struct{}

func (expiryAuthenticator) Verify(_, _, _ []byte) error { return nil }

func (expiryAuthenticator) VerifyWithContext(ctx authExt.VerifyContext, _, _, signature []byte) error {
	expiresAt, err := strconv.ParseInt(string(signature), 10, 64)
	if err != nil {
		return err
	}
	if ctx.BlockContext == nil || expiresAt <= ctx.BlockContext.Timestamp {
		return errors.New("token expired")
	}
	return nil
}

func (expiryAuthenticator) Identifier(sender []byte) (string, error) {
	return hex.EncodeToString(sender), nil
}

func (expiryAuthenticator) KeyType() crypto.KeyType { return crypto.KeyTypeEd25519 }

func registerExpiryAuthenticator(t *testing.T) {
	t.Helper()
	require.NoError(t, authExt.RegisterAuthenticator(authExt.ModAdd, expiryAuthName, expiryAuthenticator{}))
	t.Cleanup(func() {
		require.NoError(t, authExt.RegisterAuthenticator(authExt.ModRemove, expiryAuthName, nil))
	})
}

func expiringTx(sender string, expiresAt time.Time) *ktypes.Transaction {
	return &ktypes.Transaction{
		Signature: &auth.Signature{
			Type: expiryAuthName,
			Data: []byte(strconv.FormatInt(expiresAt.Unix(), 10)),
		},
		Body: &ktypes.TransactionBody{
			Description: sender,
			PayloadType: ktypes.PayloadTypeExecute,
			Payload:     []byte(`x`),
			Fee:         big.NewInt(0),
			Nonce:       1,
		},
		Sender:        edPubKey([]byte(sender)),
		Serialization: ktypes.DefaultSignedMsgSerType,
	}
}

func contextAuthTestProcessor(t *testing.T) *BlockProcessor {
	_, signer := genNodeKeyAndSigner(t)
	return &BlockProcessor{
		db:     &mockDB{},
		log:    log.DiscardLogger,
		signer: signer,
		chainCtx: &common.ChainContext{
			ChainID: "test",
			NetworkParameters: &ktypes.NetworkParameters{
				Leader:           ktypes.PublicKey{PublicKey: signer.PubKey()},
				MaxBlockSize:     6 * 1024 * 1024,
				MaxVotesPerTx:    100,
				DisabledGasCosts: true,
			},
			NetworkUpdates: ktypes.ParamUpdates{},
		},
		txapp:      &mockTxApp{},
		nodeStatus: newNodeStatus(),
	}
}

// IDOS-38: a context-bound tx that expires after admission must not survive
// a recheck.
func TestRecheckVerifiesContextBoundAuth(t *testing.T) {
	registerExpiryAuthenticator(t)
	bp := contextAuthTestProcessor(t)

	lastBlock := time.Unix(1_000_000, 0)
	ntx := nodetypes.NewTx(expiringTx("user", lastBlock.Add(10*time.Second)))
	ctx := context.Background()

	for _, recheck := range []bool{false, true} {
		require.NoError(t, bp.checkTx(ctx, &mockTx{}, ntx, 1, lastBlock, recheck))
	}

	// Next block is past the expiry.
	err := bp.checkTx(ctx, &mockTx{}, ntx, 2, lastBlock.Add(time.Minute), true)
	require.ErrorContains(t, err, "token expired")
}

// IDOS-38: the leader drops txs whose context-bound auth fails at the
// timestamp it is about to stamp on the proposal.
func TestPrepareProposalDropsTxsInvalidAtProposalTime(t *testing.T) {
	registerExpiryAuthenticator(t)
	bp := contextAuthTestProcessor(t)
	prevGetEvents := getEvents
	getEvents = func(context.Context, sql.Executor) ([]*ktypes.VotableEvent, error) { return nil, nil }
	t.Cleanup(func() { getEvents = prevGetEvents })

	stamp := time.Unix(1_000_000, 0)
	expired := expiringTx("expired", stamp) // expiry must be after the block time
	valid := expiringTx("valid", stamp.Add(time.Second))

	finalTxs, invalidTxs, err := bp.prepareBlockTransactions(context.Background(), nil, []*nodetypes.Tx{
		nodetypes.NewTx(expired),
		nodetypes.NewTx(valid),
	}, stamp)
	require.NoError(t, err)
	require.Len(t, finalTxs, 1)
	require.Equal(t, valid.Hash(), finalTxs[0].Hash())
	require.Len(t, invalidTxs, 1)
	require.Equal(t, expired.Hash(), invalidTxs[0].Hash())
}

// IDOS-38: a proposed tx whose context-bound auth fails at the block time is
// a failed tx result, not a block abort that halts the chain.
func TestExecuteBlockFailsContextAuthTxInsteadOfBlock(t *testing.T) {
	registerExpiryAuthenticator(t)
	bp := contextAuthTestProcessor(t)
	app := &countingTxApp{}
	bp.txapp = app
	bp.db = &execTestDB{}
	bp.accounts = execTestAccounts{}
	bp.validators = newValidatorStore(nil)
	bp.migrator = execTestMigrator{}

	stamp := time.Unix(1_000_000, 0)
	expired := expiringTx("expired", stamp)
	valid := expiringTx("valid", stamp.Add(time.Second))
	blk := ktypes.NewBlock(1, ktypes.Hash{}, ktypes.Hash{}, ktypes.Hash{}, ktypes.Hash{}, stamp, []*ktypes.Transaction{expired, valid})

	_, otherKey := genNodeKeyAndSigner(t)
	res, err := bp.ExecuteBlock(context.Background(), &ktypes.BlockExecRequest{
		Height:   1,
		Block:    blk,
		BlockID:  blk.Hash(),
		Proposer: otherKey.PubKey(),
	}, false)
	require.NoError(t, err)
	require.Len(t, res.TxResults, 2)

	require.Equal(t, uint32(ktypes.CodeInvalidSignature), res.TxResults[0].Code)
	require.Contains(t, res.TxResults[0].Log, "token expired")
	require.Equal(t, uint32(ktypes.CodeOk), res.TxResults[1].Code)
	require.Equal(t, []ktypes.Hash{valid.Hash()}, app.executed, "the failed tx must not be executed")
}

type countingTxApp struct {
	mockTxApp
	executed []ktypes.Hash
}

func (a *countingTxApp) Execute(_ *common.TxContext, _ sql.DB, tx *ktypes.Transaction) *txapp.TxResponse {
	a.executed = append(a.executed, tx.Hash())
	return &txapp.TxResponse{ResponseCode: ktypes.CodeOk}
}

// execTestDB returns a tx whose statements report one affected row, which is
// what the meta store writes in ExecuteBlock expect.
type execTestDB struct{ mockDB }

func (execTestDB) BeginPreparedTx(context.Context) (sql.PreparedTx, error) {
	return execTestTx{&mockTx{}}, nil
}

type execTestTx struct{ *mockTx }

func (execTestTx) Execute(context.Context, string, ...any) (*sql.ResultSet, error) {
	return &sql.ResultSet{Status: sql.CommandTag{RowsAffected: 1}}, nil
}

func (execTestTx) BeginTx(context.Context) (sql.Tx, error) { return execTestTx{&mockTx{}}, nil }

func (execTestTx) Precommit(_ context.Context, changes chan<- any) ([]byte, error) {
	close(changes)
	return make([]byte, 32), nil
}

type execTestAccounts struct{}

func (execTestAccounts) Updates() []*ktypes.Account { return nil }

type execTestMigrator struct{ MigratorModule }

func (execTestMigrator) NotifyHeight(context.Context, *common.BlockContext, migrations.Database, sql.Executor) error {
	return nil
}
