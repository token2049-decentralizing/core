package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstallCRELogin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CRE_API_KEY", "")
	t.Setenv("CRE_LOGIN_YAML", "")
	t.Setenv("CRE_CONTEXT_YAML", "")

	mode, err := installCRELogin(home)
	require.NoError(t, err)
	require.Equal(t, "none", mode)

	t.Setenv("CRE_LOGIN_YAML", base64.StdEncoding.EncodeToString([]byte("RefreshToken: env\n")))
	t.Setenv("CRE_CONTEXT_YAML", base64.StdEncoding.EncodeToString([]byte("PRODUCTION: {}\n")))
	mode, err = installCRELogin(home)
	require.NoError(t, err)
	require.Contains(t, mode, "seeded")
	b, err := os.ReadFile(filepath.Join(home, ".cre", "cre.yaml"))
	require.NoError(t, err)
	require.Equal(t, "RefreshToken: env\n", string(b))
	info, err := os.Stat(filepath.Join(home, ".cre", "cre.yaml"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.FileExists(t, filepath.Join(home, ".cre", "context.yaml"))

	// A session the CLI refreshed in place (or a developer's own ~/.cre) is never overwritten.
	require.NoError(t, os.WriteFile(filepath.Join(home, ".cre", "cre.yaml"), []byte("RefreshToken: newer\n"), 0o600))
	mode, err = installCRELogin(home)
	require.NoError(t, err)
	require.Equal(t, "login_session", mode)
	b, _ = os.ReadFile(filepath.Join(home, ".cre", "cre.yaml"))
	require.Equal(t, "RefreshToken: newer\n", string(b))

	t.Setenv("CRE_API_KEY", "k")
	mode, err = installCRELogin(t.TempDir())
	require.NoError(t, err)
	require.Equal(t, "api_key", mode)

	t.Setenv("CRE_API_KEY", "")
	t.Setenv("CRE_LOGIN_YAML", "not base64!")
	_, err = installCRELogin(t.TempDir())
	require.ErrorContains(t, err, "base64")
}
