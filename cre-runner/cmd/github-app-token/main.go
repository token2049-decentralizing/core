// github-app-token mints a short-lived (1h), read-only GitHub App installation token and prints it.
//
// Usage: go run ./cmd/github-app-token
// Env: GITHUB_APP_ID, GITHUB_APP_PRIVATE_KEY_PATH, and GITHUB_APP_INSTALLATION_ID or GITHUB_APP_REPO ("owner/repo").
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/token2049-decentralizing/core/cre-runner/internal/ghapp"
)

func main() {
	m, err := ghapp.FromEnv()
	if err == nil {
		var token string
		if token, err = m.Token(context.Background()); err == nil {
			fmt.Print(token)
			return
		}
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
