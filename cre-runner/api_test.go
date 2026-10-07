package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/supabase-community/supabase-go"
)

func TestListPRExecutionsValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := supabase.NewClient("http://127.0.0.1:1", "test-key", nil)
	require.NoError(t, err)
	r := gin.New()
	registerAPI(r, db)

	const campaign = "11111111-1111-1111-1111-111111111111"
	for path, status := range map[string]int{
		"/api/campaigns/not-a-uuid/repos/acme/pool/prs/7/executions":                     http.StatusNotFound,
		"/api/campaigns/" + campaign + "/repos/acme/pool/prs/0/executions":               http.StatusBadRequest,
		"/api/campaigns/" + campaign + "/repos/acme/pool/prs/x/executions":               http.StatusBadRequest,
		"/api/campaigns/" + campaign + "/repos/ac%20me/pool/prs/7/executions":            http.StatusBadRequest,
		"/api/campaigns/" + campaign + "/repos/acme/pool/prs/7/executions?page_size=500": http.StatusBadRequest,
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, status, w.Code, path)
		require.Contains(t, w.Body.String(), `"error"`, path)
	}
}
