package pgp

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewChallengeString_PrefixedAndURLSafe(t *testing.T) {
	challenge, err := NewChallengeString(0)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(challenge, VerificationChallengePrefix))

	suffix := strings.TrimPrefix(challenge, VerificationChallengePrefix)
	require.Equal(t, base64.RawURLEncoding.EncodedLen(DefaultChallengeSize), len(suffix))

	for _, r := range suffix {
		isUpper := r >= 'A' && r <= 'Z'
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		isDash := r == '-'
		isUnderscore := r == '_'
		require.True(t, isUpper || isLower || isDigit || isDash || isUnderscore)
	}
}

func TestNewChallengeString_RespectsSize(t *testing.T) {
	challenge, err := NewChallengeString(1)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(challenge, VerificationChallengePrefix))

	suffix := strings.TrimPrefix(challenge, VerificationChallengePrefix)
	require.Equal(t, base64.RawURLEncoding.EncodedLen(1), len(suffix))
}
