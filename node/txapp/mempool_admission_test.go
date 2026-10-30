package txapp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/trufnetwork/kwil-db/common"
	"github.com/trufnetwork/kwil-db/core/log"
	"github.com/trufnetwork/kwil-db/core/types"
	"github.com/trufnetwork/kwil-db/extensions/precompiles"
)

type mockAdmissionEngine struct {
	owner   string
	actions map[string]precompiles.Modifiers // keyed by namespace + "." + action
}

func (e *mockAdmissionEngine) ActionModifiers(namespace, action string) (precompiles.Modifiers, bool) {
	mods, ok := e.actions[namespace+"."+action]
	return mods, ok
}

func (e *mockAdmissionEngine) IsOwner(user string) bool {
	return user == e.owner
}

func admissionTx(t *testing.T, payload types.Payload) *types.Transaction {
	t.Helper()
	bts, err := payload.MarshalBinary()
	require.NoError(t, err)

	tx := newTx(t, 1, "A")
	tx.Body.PayloadType = payload.Type()
	tx.Body.Payload = bts
	return tx
}

func Test_MempoolAdmission(t *testing.T) {
	engine := &mockAdmissionEngine{
		owner: "owner",
		actions: map[string]precompiles.Modifiers{
			"main.add_wallet":   {precompiles.PUBLIC},
			"main.get_wallets":  {precompiles.PUBLIC, precompiles.VIEW},
			"main.helper":       {precompiles.PRIVATE},
			"main.internal":     {precompiles.SYSTEM},
			"main.admin_only":   {precompiles.PUBLIC, precompiles.OWNER},
			"idos.eip55":        {precompiles.PUBLIC, precompiles.VIEW},
			"bridge.vote_epoch": {precompiles.PUBLIC},
		},
	}

	exec := func(namespace, action string) types.Payload {
		return &types.ActionExecution{Namespace: namespace, Action: action}
	}
	raw := &types.RawStatement{Statement: "SELECT 1;"}
	resolution := &types.CreateResolution{Resolution: &types.VotableEvent{Type: "test", Body: []byte("body")}}

	tests := []struct {
		name      string
		caller    string
		validator bool
		payload   types.Payload
		admitted  bool
	}{
		{"public action", "user", false, exec("main", "add_wallet"), true},
		{"extension method", "user", false, exec("bridge", "vote_epoch"), true},
		{"view action", "user", false, exec("main", "get_wallets"), false},
		{"view precompile", "user", false, exec("idos", "eip55"), false},
		{"private action", "user", false, exec("main", "helper"), false},
		{"system action", "user", false, exec("main", "internal"), false},
		{"owner action by user", "user", false, exec("main", "admin_only"), false},
		{"unknown action", "user", false, exec("main", "nope"), false},
		{"unknown namespace", "user", false, exec("other", "add_wallet"), false},
		{"raw statement by user", "user", false, raw, false},
		{"raw statement by owner", "owner", false, raw, true},
		{"view action by owner", "owner", false, exec("main", "get_wallets"), true},
		{"owner action by owner", "owner", false, exec("main", "admin_only"), true},
		{"validator leave by user", "user", false, &types.ValidatorLeave{}, false},
		{"validator leave by validator", "user", true, &types.ValidatorLeave{}, true},
		{"approve resolution by user", "user", false, &types.ApproveResolution{ResolutionID: &types.UUID{}}, false},
		{"create resolution by user", "user", false, resolution, false},
		{"create resolution by validator", "user", true, resolution, true},
		{"validator join by user", "user", false, &types.ValidatorJoin{Power: 1}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			power := int64(0)
			if tt.validator {
				power = 1
			}
			m := &mempool{
				accounts:     make(map[string]*types.Account),
				accountMgr:   &mockAccount{},
				validatorMgr: &mockValidator{getVoterFn: func() (int64, error) { return power, nil }},
				engine:       engine,
				log:          log.DiscardLogger,
			}

			txCtx := &common.TxContext{
				Ctx:    context.Background(),
				Caller: tt.caller,
				BlockContext: &common.BlockContext{
					ChainContext: &common.ChainContext{
						NetworkParameters: &types.NetworkParameters{DisabledGasCosts: true},
					},
				},
			}

			err := m.applyTransaction(txCtx, admissionTx(t, tt.payload), &mockDb{}, &mockRebroadcast{})
			if tt.admitted {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.ErrTxNotAdmitted)
			require.Equal(t, types.CodeInvalidTxType, types.BroadcastErrorToCode(err))
		})
	}
}

func Test_MempoolAdmissionWithoutEngine(t *testing.T) {
	m := &mempool{
		accounts:   make(map[string]*types.Account),
		accountMgr: &mockAccount{},
		log:        log.DiscardLogger,
	}
	txCtx := &common.TxContext{
		Ctx:    context.Background(),
		Caller: "user",
		BlockContext: &common.BlockContext{
			ChainContext: &common.ChainContext{
				NetworkParameters: &types.NetworkParameters{DisabledGasCosts: true},
			},
		},
	}

	tx := admissionTx(t, &types.RawStatement{Statement: "SELECT 1;"})
	require.NoError(t, m.applyTransaction(txCtx, tx, &mockDb{}, &mockRebroadcast{}))
}
