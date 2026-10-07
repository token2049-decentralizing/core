package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidEVMKey(t *testing.T) {
	require.True(t, validEVMKey("4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"))
	require.True(t, validEVMKey("0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"))
	for _, bad := range []string{
		"", "abc",
		strings.Repeat("0", 63) + "1", // the cre CLI's default key
		strings.Repeat("0", 64),
		strings.Repeat("z", 64),
	} {
		require.False(t, validEVMKey(bad), bad)
	}
}
