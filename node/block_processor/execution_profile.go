package blockprocessor

import (
	"fmt"
	"sort"
	"time"

	ktypes "github.com/trufnetwork/kwil-db/core/types"
)

const (
	// ponytail: fixed thresholds keep profiling zero-config; make them configurable only if production volume requires it.
	slowBlockTransactionExecutionThreshold = 5 * time.Second
	slowTransactionExecutionThreshold      = time.Second
)

type executionProfileKey struct {
	payloadType string
	namespace   string
	action      string
}

type actionExecutionAccumulator struct {
	transactions  int
	calls         int
	failures      int
	total         time.Duration
	max           time.Duration
	payloadTotal  time.Duration
	payloadMax    time.Duration
	overheadTotal time.Duration
	overheadMax   time.Duration
}

type actionExecutionProfile struct {
	PayloadType   string `json:"payload_type"`
	Namespace     string `json:"namespace,omitempty"`
	Action        string `json:"action,omitempty"`
	Transactions  int    `json:"transactions"`
	Calls         int    `json:"calls"`
	Failures      int    `json:"failures"`
	TotalMs       int64  `json:"total_ms"`
	MaxMs         int64  `json:"max_ms"`
	PayloadMs     int64  `json:"payload_ms"`
	MaxPayloadMs  int64  `json:"max_payload_ms"`
	OverheadMs    int64  `json:"overhead_ms"`
	MaxOverheadMs int64  `json:"max_overhead_ms"`
}

func (p actionExecutionProfile) String() string {
	return fmt.Sprintf(
		"{payload_type=%s namespace=%s action=%s transactions=%d calls=%d failures=%d total_ms=%d payload_ms=%d overhead_ms=%d max_ms=%d max_payload_ms=%d max_overhead_ms=%d}",
		p.PayloadType,
		p.Namespace,
		p.Action,
		p.Transactions,
		p.Calls,
		p.Failures,
		p.TotalMs,
		p.PayloadMs,
		p.OverheadMs,
		p.MaxMs,
		p.MaxPayloadMs,
		p.MaxOverheadMs,
	)
}

type slowTransactionExecution struct {
	Hash        string `json:"hash"`
	PayloadType string `json:"payload_type"`
	Namespace   string `json:"namespace,omitempty"`
	Action      string `json:"action,omitempty"`
	Code        uint32 `json:"code"`
	DurationMs  int64  `json:"duration_ms"`
	PayloadMs   int64  `json:"payload_ms"`
	OverheadMs  int64  `json:"overhead_ms"`
}

func (p slowTransactionExecution) String() string {
	return fmt.Sprintf(
		"{hash=%s payload_type=%s namespace=%s action=%s code=%d duration_ms=%d payload_ms=%d overhead_ms=%d}",
		p.Hash,
		p.PayloadType,
		p.Namespace,
		p.Action,
		p.Code,
		p.DurationMs,
		p.PayloadMs,
		p.OverheadMs,
	)
}

type blockExecutionProfile struct {
	total   time.Duration
	actions map[executionProfileKey]*actionExecutionAccumulator
	slowTxs []slowTransactionExecution
}

func newBlockExecutionProfile() *blockExecutionProfile {
	return &blockExecutionProfile{
		actions: make(map[executionProfileKey]*actionExecutionAccumulator),
	}
}

func (p *blockExecutionProfile) record(
	tx *ktypes.Transaction,
	txHash ktypes.Hash,
	code uint32,
	duration time.Duration,
	payloadDuration time.Duration,
) {
	key, calls := executionAction(tx)
	action := p.actions[key]
	if action == nil {
		action = &actionExecutionAccumulator{}
		p.actions[key] = action
	}
	action.transactions++
	action.calls += calls
	action.total += duration
	if duration > action.max {
		action.max = duration
	}
	overheadDuration := duration - payloadDuration
	if overheadDuration < 0 {
		overheadDuration = 0
	}
	action.payloadTotal += payloadDuration
	if payloadDuration > action.payloadMax {
		action.payloadMax = payloadDuration
	}
	action.overheadTotal += overheadDuration
	if overheadDuration > action.overheadMax {
		action.overheadMax = overheadDuration
	}
	if code != uint32(ktypes.CodeOk) {
		action.failures++
	}
	p.total += duration
	if duration >= slowTransactionExecutionThreshold {
		p.slowTxs = append(p.slowTxs, slowTransactionExecution{
			Hash:        txHash.String(),
			PayloadType: key.payloadType,
			Namespace:   key.namespace,
			Action:      key.action,
			Code:        code,
			DurationMs:  duration.Milliseconds(),
			PayloadMs:   payloadDuration.Milliseconds(),
			OverheadMs:  overheadDuration.Milliseconds(),
		})
	}
}

func (p *blockExecutionProfile) shouldLog() bool {
	return p.total >= slowBlockTransactionExecutionThreshold || len(p.slowTxs) > 0
}

func (p *blockExecutionProfile) actionProfiles() []actionExecutionProfile {
	profiles := make([]actionExecutionProfile, 0, len(p.actions))
	for key, action := range p.actions {
		profiles = append(profiles, actionExecutionProfile{
			PayloadType:   key.payloadType,
			Namespace:     key.namespace,
			Action:        key.action,
			Transactions:  action.transactions,
			Calls:         action.calls,
			Failures:      action.failures,
			TotalMs:       action.total.Milliseconds(),
			MaxMs:         action.max.Milliseconds(),
			PayloadMs:     action.payloadTotal.Milliseconds(),
			MaxPayloadMs:  action.payloadMax.Milliseconds(),
			OverheadMs:    action.overheadTotal.Milliseconds(),
			MaxOverheadMs: action.overheadMax.Milliseconds(),
		})
	}
	sort.Slice(profiles, func(i, j int) bool {
		left, right := profiles[i], profiles[j]
		if left.TotalMs != right.TotalMs {
			return left.TotalMs > right.TotalMs
		}
		if left.PayloadType != right.PayloadType {
			return left.PayloadType < right.PayloadType
		}
		if left.Namespace != right.Namespace {
			return left.Namespace < right.Namespace
		}
		return left.Action < right.Action
	})
	return profiles
}

func (p *blockExecutionProfile) slowTransactions() []slowTransactionExecution {
	slowTxs := append([]slowTransactionExecution(nil), p.slowTxs...)
	sort.Slice(slowTxs, func(i, j int) bool {
		return slowTxs[i].DurationMs > slowTxs[j].DurationMs
	})
	return slowTxs
}

func executionAction(tx *ktypes.Transaction) (executionProfileKey, int) {
	key := executionProfileKey{payloadType: tx.Body.PayloadType.String()}
	calls := 1
	if tx.Body.PayloadType != ktypes.PayloadTypeExecute {
		return key, calls
	}
	payload, err := ktypes.UnmarshalPayload(tx.Body.PayloadType, tx.Body.Payload)
	if err != nil {
		key.action = fmt.Sprintf("<decode error: %s>", err)
		return key, calls
	}
	execution, ok := payload.(*ktypes.ActionExecution)
	if !ok {
		key.action = fmt.Sprintf("<unexpected payload %T>", payload)
		return key, calls
	}
	key.namespace = execution.Namespace
	key.action = execution.Action
	if len(execution.Arguments) > 0 {
		calls = len(execution.Arguments)
	}
	return key, calls
}
