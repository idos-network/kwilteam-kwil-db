package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisteredAuthType_caseVariantResolvesToRegisteredName(t *testing.T) {
	got, err := RegisteredAuthType("ED25519")
	require.NoError(t, err)
	require.Equal(t, "ed25519", got)

	require.NoError(t, ValidateCanonicalAuthType("ed25519"))

	err = ValidateCanonicalAuthType("ED25519")
	require.ErrorIs(t, err, ErrNonCanonicalAuthType)

	_, err = RegisteredAuthType("not-an-auth")
	require.ErrorIs(t, err, ErrAuthenticatorNotFound)
}
