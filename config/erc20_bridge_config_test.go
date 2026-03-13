package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestERC20BridgeConfig_Validate_BlockSyncChuckSize(t *testing.T) {
	base := func() ERC20BridgeConfig {
		return ERC20BridgeConfig{
			RPC: map[string]string{
				"ethereum": "ws://localhost:8546",
			},
		}
	}

	t.Run("valid block_sync_chuck_size", func(t *testing.T) {
		cfg := base()
		cfg.BlockSyncChuckSize = map[string]string{"ethereum": "500000"}
		require.NoError(t, cfg.Validate())
	})

	t.Run("no block_sync_chuck_size is fine", func(t *testing.T) {
		cfg := base()
		require.NoError(t, cfg.Validate())
	})

	t.Run("invalid chain in block_sync_chuck_size", func(t *testing.T) {
		cfg := base()
		cfg.BlockSyncChuckSize = map[string]string{"fake_chain": "100000"}
		err := cfg.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "erc20_bridge.block_sync_chuck_size")
		require.Contains(t, err.Error(), "invalid chain")
	})

	t.Run("non-canonical chain key in block_sync_chuck_size", func(t *testing.T) {
		cfg := base()
		cfg.BlockSyncChuckSize = map[string]string{"Ethereum": "100000"}
		err := cfg.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "canonical chain name")
	})

	t.Run("non-numeric block_sync_chuck_size value", func(t *testing.T) {
		cfg := base()
		cfg.BlockSyncChuckSize = map[string]string{"ethereum": "abc"}
		err := cfg.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "erc20_bridge.block_sync_chuck_size")
		require.Contains(t, err.Error(), "invalid value")
	})

	t.Run("zero block_sync_chuck_size", func(t *testing.T) {
		cfg := base()
		cfg.BlockSyncChuckSize = map[string]string{"ethereum": "0"}
		err := cfg.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "must be greater than 0")
	})

	t.Run("negative block_sync_chuck_size", func(t *testing.T) {
		cfg := base()
		cfg.BlockSyncChuckSize = map[string]string{"ethereum": "-1"}
		err := cfg.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "must be greater than 0")
	})
}
