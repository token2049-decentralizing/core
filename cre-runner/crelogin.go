package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// creLoginFiles maps env vars to the files `cre login` writes in ~/.cre. Values are base64
// (scripts/cre-login-env.sh prints them from your own ~/.cre).
var creLoginFiles = []struct{ env, file string }{
	{"CRE_LOGIN_YAML", "cre.yaml"},       // OAuth session: access, ID and refresh token
	{"CRE_CONTEXT_YAML", "context.yaml"}, // tenant context (optional; the CLI can fetch it)
}

// installCRELogin seeds $HOME/.cre from CRE_LOGIN_YAML / CRE_CONTEXT_YAML so the CLI can use a
// `cre login` session without CRE_API_KEY (e.g. on Fly, where ~/.cre cannot be mounted).
// Existing files are kept: the CLI refreshes the 15-minute access token in place, so a
// persisted session is newer than the env copy, and a developer's own ~/.cre is never overwritten.
// It returns the CLI auth mode for the startup log.
func installCRELogin(home string) (string, error) {
	if os.Getenv("CRE_API_KEY") != "" {
		return "api_key", nil // The CLI prefers the API key over a login session.
	}
	dir := filepath.Join(home, ".cre")
	seeded := false
	for _, f := range creLoginFiles {
		v := os.Getenv(f.env)
		if v == "" {
			continue
		}
		path := filepath.Join(dir, f.file)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return "", fmt.Errorf("%s must be base64 (see scripts/cre-login-env.sh): %w", f.env, err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return "", err
		}
		seeded = true
	}
	if _, err := os.Stat(filepath.Join(dir, "cre.yaml")); err != nil {
		return "none", nil
	}
	if seeded {
		return "login_session (seeded from CRE_LOGIN_YAML)", nil
	}
	return "login_session", nil
}
