package consensus

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValidateProposalTimestamp(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000).UTC()
	prev := now.Add(-5 * time.Second)

	tests := []struct {
		name    string
		stamp   time.Time
		prev    time.Time
		wantErr bool
	}{
		{"now", now, prev, false},
		{"1ms after previous", prev.Add(time.Millisecond), prev, false},
		{"at drift limit", now.Add(maxProposalClockDrift), prev, false},
		{"no previous block", now, time.Time{}, false},
		{"equal to previous", prev, prev, true},
		{"before previous", prev.Add(-time.Millisecond), prev, true},
		{"past drift limit", now.Add(maxProposalClockDrift + time.Millisecond), prev, true},
		{"hours ahead", now.Add(6 * time.Hour), prev, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProposalTimestamp(tt.stamp, tt.prev, now)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestProposalTimestamp(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000).Add(123 * time.Microsecond)

	// Local clock past the previous block: use it, truncated to milliseconds.
	stamp := proposalTimestamp(now, now.Add(-time.Second))
	require.Equal(t, now.Truncate(time.Millisecond).UTC(), stamp)

	// No previous block.
	require.Equal(t, now.Truncate(time.Millisecond).UTC(), proposalTimestamp(now, time.Time{}))

	// Local clock behind or equal to the previous block: step 1ms past it so
	// followers still see an increasing timestamp.
	prev := now.Truncate(time.Millisecond).Add(2 * time.Second)
	stamp = proposalTimestamp(now, prev)
	require.Equal(t, prev.Add(time.Millisecond).UTC(), stamp)
	require.NoError(t, validateProposalTimestamp(stamp, prev, now))

	stamp = proposalTimestamp(prev, prev)
	require.Equal(t, prev.Add(time.Millisecond).UTC(), stamp)
}
