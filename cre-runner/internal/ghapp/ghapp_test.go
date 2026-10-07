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

func TestPoolLooksUpInstallationPerRepo(t *testing.T) {
	var lookups atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/pool/installation", "/repos/other/lib/installation":
			lookups.Add(1)
			id := map[string]string{"/repos/acme/pool/installation": "1", "/repos/other/lib/installation": "2"}[r.URL.Path]
			_, _ = w.Write([]byte(`{"id":` + id + `}`))
		case "/app/installations/1/access_tokens", "/app/installations/2/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token": "ghs_inst" + strings.Split(r.URL.Path, "/")[3], "expires_at": time.Now().Add(time.Hour),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewPool(&Minter{APIURL: srv.URL, AppID: "1", Key: testKey(t)})
	tok, err := p.ForRepo("acme/pool").Token(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ghs_inst1", tok)
	tok, err = p.ForRepo("other/lib").Token(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ghs_inst2", tok)
	_, _ = p.ForRepo("acme/pool").Token(context.Background()) // Cached minter, cached token.
	require.Equal(t, int32(2), lookups.Load())

	fixed := &Minter{AppID: "1", InstallationID: "9"}
	require.Same(t, fixed, NewPool(fixed).ForRepo("any/repo"))
}

func TestWithPermissionsScopesEveryRepoToken(t *testing.T) {
	write := map[string]string{"pull_requests": "write", "metadata": "read"}
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/pool/installation":
			_, _ = w.Write([]byte(`{"id":1}`))
		case "/app/installations/1/access_tokens":
			var body map[string]map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			got = body["permissions"]
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_w", "expires_at": time.Now().Add(time.Hour)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	read := NewPool(&Minter{APIURL: srv.URL, AppID: "1", Key: testKey(t)})
	_, err := WithPermissions(read, write).ForRepo("acme/pool").Token(context.Background())
	require.NoError(t, err)
	require.Equal(t, write, got)
	_, err = read.ForRepo("acme/pool").Token(context.Background()) // The original pool stays read-only.
	require.NoError(t, err)
	require.Equal(t, ReadOnly, got)

	require.Equal(t, Static("t"), WithPermissions(Static("t"), write))
}

func TestAppFromEnvKeyContents(t *testing.T) {
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(testKey(t))})
	t.Setenv("GITHUB_APP_ID", "1")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", string(pemBytes))
	t.Setenv("GITHUB_APP_PRIVATE_KEY_PATH", "/nonexistent.pem") // Contents win.
	tokens, err := SourceFromEnv()
	require.NoError(t, err)
	require.IsType(t, &Pool{}, tokens)

	t.Setenv("GITHUB_APP_PRIVATE_KEY", "")
	_, err = SourceFromEnv()
	require.Error(t, err)
}
