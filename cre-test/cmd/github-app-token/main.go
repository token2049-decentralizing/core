// github-app-token mints a short-lived (1h), read-only GitHub App installation token and prints it.
//
// Usage: go run ./cmd/github-app-token
// Env: GITHUB_APP_ID, GITHUB_APP_PRIVATE_KEY_PATH, and GITHUB_APP_INSTALLATION_ID or GITHUB_APP_REPO ("owner/repo").
package main

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Only what the workflow reads. Token is downscoped even if the App has more.
var readOnly = map[string]string{"pull_requests": "read", "contents": "read", "checks": "read", "metadata": "read"}

func parseKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("private key is not PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return rk, nil
}

// appJWT builds an RS256 App JWT, valid 9 min. iat is backdated 60s for clock drift.
func appJWT(appID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat": now.Unix() - 60, "exp": now.Unix() + 540, "iss": appID})
	signingInput := header + "." + enc.EncodeToString(claims)

	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + enc.EncodeToString(sig), nil
}

func gh(api, path, jwt string, body any) (map[string]any, error) {
	method, reader := http.MethodGet, io.Reader(nil)
	if body != nil {
		b, _ := json.Marshal(body)
		method, reader = http.MethodPost, bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, api+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "contriboracle")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("GitHub %s -> HTTP %d: %s", path, res.StatusCode, raw)
	}
	var out map[string]any
	return out, json.Unmarshal(raw, &out)
}

func need(name string) string {
	v := os.Getenv(name)
	if v == "" {
		fmt.Fprintf(os.Stderr, "missing env %s\n", name)
		os.Exit(1)
	}
	return v
}

func run() (string, error) {
	api := os.Getenv("GITHUB_API_URL")
	if api == "" {
		api = "https://api.github.com"
	}
	pemBytes, err := os.ReadFile(need("GITHUB_APP_PRIVATE_KEY_PATH"))
	if err != nil {
		return "", err
	}
	key, err := parseKey(pemBytes)
	if err != nil {
		return "", err
	}
	jwt, err := appJWT(need("GITHUB_APP_ID"), key, time.Now())
	if err != nil {
		return "", err
	}

	installationID := os.Getenv("GITHUB_APP_INSTALLATION_ID")
	if installationID == "" {
		inst, err := gh(api, "/repos/"+need("GITHUB_APP_REPO")+"/installation", jwt, nil)
		if err != nil {
			return "", err
		}
		installationID = fmt.Sprintf("%.0f", inst["id"])
	}

	res, err := gh(api, "/app/installations/"+installationID+"/access_tokens", jwt, map[string]any{"permissions": readOnly})
	if err != nil {
		return "", err
	}
	token, _ := res["token"].(string)
	if token == "" {
		return "", errors.New("GitHub returned no token")
	}
	return token, nil
}

func main() {
	token, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(token)
}
