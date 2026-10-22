package txapp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestResolutionExpired(t *testing.T) {
	expiration := time.Unix(1_700_000_000, 0)
	require.False(t, resolutionExpired(expiration, expiration.Unix()-1))
	require.True(t, resolutionExpired(expiration, expiration.Unix()))
	require.True(t, resolutionExpired(expiration, expiration.Unix()+1))
}
