package blockprocessor

import (
	"bytes"
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/trufnetwork/kwil-db/core/crypto"
	"github.com/trufnetwork/kwil-db/core/crypto/auth"
	ktypes "github.com/trufnetwork/kwil-db/core/types"
)

// ethUserTx is an execute tx signed the way kwil-cli signs for a user:
// secp256k1_ep, with the Ethereum address as Sender.
func ethUserTx(t *testing.T, signer auth.Signer, nonce uint64) *ktypes.Transaction {
	t.Helper()
	tx := &ktypes.Transaction{
		Body: &ktypes.TransactionBody{
			Description: "user tx",
			PayloadType: ktypes.PayloadTypeExecute,
			Payload:     []byte(`x`),
			Fee:         big.NewInt(0),
			Nonce:       nonce,
			ChainID:     "test",
		},
		Serialization: ktypes.DefaultSignedMsgSerType,
	}
	require.NoError(t, tx.Sign(signer))
	return tx
}

func genEthUserSigner(t *testing.T) auth.Signer {
	t.Helper()
	key, err := crypto.GeneratePrivateKey(crypto.KeyTypeSecp256k1)
	require.NoError(t, err)
	signer := auth.GetUserSigner(key)
	require.NotNil(t, signer)
	return signer
}

// badSigTxs returns txs that name user as Sender without a valid signature by
// user.
func badSigTxs(t *testing.T, user auth.Signer) (junkSig, otherKeySig, noBody *ktypes.Transaction) {
	t.Helper()

	// 65 bytes that are not a signature by user.
	junkSig = ethUserTx(t, user, 2)
	junkSig.Signature = &auth.Signature{
		Type: auth.EthPersonalSignAuth,
		Data: bytes.Repeat([]byte{0x01}, crypto.Secp256k1SignatureLength),
	}

	// A valid signature by another key over the same tx.
	otherKeySig = ethUserTx(t, genEthUserSigner(t), 3)
	otherKeySig.Sender = user.CompactID()

	// A block tx can be decoded without a body. It must fail, not panic.
	noBody = &ktypes.Transaction{
		Signature:     junkSig.Signature,
		Sender:        user.CompactID(),
		Serialization: ktypes.DefaultSignedMsgSerType,
	}

	return junkSig, otherKeySig, noBody
}

func execTestProcessor(t *testing.T) (*BlockProcessor, *countingTxApp) {
	bp := contextAuthTestProcessor(t)
	app := &countingTxApp{}
	bp.txapp = app
	bp.db = &execTestDB{}
	bp.accounts = execTestAccounts{}
	bp.validators = newValidatorStore(nil)
	bp.migrator = execTestMigrator{}
	return bp, app
}

func executeTestBlock(t *testing.T, bp *BlockProcessor, height int64, txs ...*ktypes.Transaction) *ktypes.BlockExecResult {
	t.Helper()
	blk := ktypes.NewBlock(height, ktypes.Hash{}, ktypes.Hash{}, ktypes.Hash{}, ktypes.Hash{}, time.Unix(1_000_000, 0), txs)

	// The proposer is another node: this processor acts as a follower.
	_, leader := genNodeKeyAndSigner(t)
	res, err := bp.ExecuteBlock(context.Background(), &ktypes.BlockExecRequest{
		Height:   height,
		Block:    blk,
		BlockID:  blk.Hash(),
		Proposer: leader.PubKey(),
	}, false)
	require.NoError(t, err)
	require.Len(t, res.TxResults, len(txs))
	return res
}

// A follower must not execute a block tx whose Sender is a user
// (or any user) when the signature does not verify for that Sender. The tx
// fails; the block and the honestly signed tx still run.
func TestExecuteBlockFailsTxWithInvalidSignature(t *testing.T) {
	user := genEthUserSigner(t)
	honest := ethUserTx(t, user, 1)
	junkSig, otherKeySig, noBody := badSigTxs(t, user)

	bp, app := execTestProcessor(t)
	res := executeTestBlock(t, bp, 1, junkSig, otherKeySig, noBody, honest)

	for i, name := range []string{"junk signature", "signature by another key", "no body"} {
		require.Equal(t, uint32(ktypes.CodeInvalidSignature), res.TxResults[i].Code, name)
		require.Contains(t, res.TxResults[i].Log, "failed to verify block tx", name)
	}
	require.Equal(t, uint32(ktypes.CodeOk), res.TxResults[3].Code)
	require.Equal(t, []ktypes.Hash{honest.Hash()}, app.executed, "a tx with an invalid signature must not be executed")
}
