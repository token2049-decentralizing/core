package ghapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

func TestAppJWT(t *testing.T) {
	key := testKey(t)

	// GitHub ships PKCS#1 ("BEGIN RSA PRIVATE KEY").
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	parsed, err := ParseKey(pemBytes)
	require.NoError(t, err)

	jwt, err := AppJWT("12345", parsed, time.Unix(1000, 0))
	require.NoError(t, err)
	parts := strings.Split(jwt, ".")
	require.Len(t, parts, 3)

	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var c map[string]any
	require.NoError(t, json.Unmarshal(claims, &c))
	require.Equal(t, map[string]any{"iat": 940.0, "exp": 1540.0, "iss": "12345"}, c)

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	require.NoError(t, rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig))
}

func TestTokenLookupAndCache(t *testing.T) {
	var mints atomic.Int32
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey"))
		switch r.URL.Path {
		case "/repos/acme/pool/installation":
			_, _ = w.Write([]byte(`{"id":42}`))
		case "/app/installations/42/access_tokens":
			var body map[string]map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, ReadOnly, body["permissions"])
			n := mints.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token": "ghs_" + string(rune('0'+n)), "expires_at": now.Add(time.Hour),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	clock := now
	m := &Minter{APIURL: srv.URL, AppID: "1", Key: testKey(t), Repo: "acme/pool", Now: func() time.Time { return clock }}

	tok, err := m.Token(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ghs_1", tok)

	clock = now.Add(50 * time.Minute) // still >5 min left: cached
	tok, _ = m.Token(context.Background())
	require.Equal(t, "ghs_1", tok)

	clock = now.Add(56 * time.Minute) // inside refresh margin: re-mint
	tok, err = m.Token(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ghs_2", tok)
	require.Equal(t, int32(2), mints.Load())
}

func TestTokenNotInstalled(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	m := &Minter{APIURL: srv.URL, AppID: "1", Key: testKey(t), Repo: "acme/pool"}
	_, err := m.Token(context.Background())
	require.ErrorContains(t, err, "HTTP 404")
}
