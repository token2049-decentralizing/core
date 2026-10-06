package main

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

var commitSHARe = regexp.MustCompile(`^[0-9a-f]{40}$`)

type evaluationsAPI struct {
	evals    *evaluations // nil: EVALUATOR_URL not set, evaluations disabled
	store    evalStore
	adminKey string
}

func registerEvaluationsAPI(r *gin.Engine, evals *evaluations, store evalStore, adminKey string) {
	a := &evaluationsAPI{evals: evals, store: store, adminKey: adminKey}
	g := r.Group("/api")
	g.GET("/evaluations", a.list)
	g.GET("/evaluations/:id", a.get)
	g.POST("/evaluations", a.create)
}

type createEvaluationRequest struct {
	CampaignID string `json:"campaign_id"`
	Repository string `json:"repository"`
	PRNumber   int    `json:"pr_number"`
	Event      string `json:"event"`    // default "opened"
	HeadSHA    string `json:"head_sha"` // optional
}

// authorized: manual runs cost LLM calls and can settle rewards, so they need ADMIN_API_KEY.
func (a *evaluationsAPI) authorized(c *gin.Context) bool {
	got, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
	return ok && a.adminKey != "" && subtle.ConstantTimeCompare([]byte(got), []byte(a.adminKey)) == 1
}

// POST /api/evaluations
func (a *evaluationsAPI) create(c *gin.Context) {
	if !a.authorized(c) {
		apiError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	if a.evals == nil {
		apiError(c, http.StatusServiceUnavailable, "evaluations are disabled (EVALUATOR_URL not set)")
		return
	}
	var req createEvaluationRequest
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		apiError(c, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if req.Event == "" {
		req.Event = "opened"
	}
	switch {
	case !uuidRe.MatchString(req.CampaignID):
		apiError(c, http.StatusBadRequest, "campaign_id must be a campaign UUID")
		return
	case !repoFullNameRe.MatchString(req.Repository):
		apiError(c, http.StatusBadRequest, "repository must be owner/repo")
		return
	case req.PRNumber <= 0:
		apiError(c, http.StatusBadRequest, "pr_number must be a positive integer")
		return
	case req.Event != "opened" && req.Event != "merged":
		apiError(c, http.StatusBadRequest, "event must be opened or merged")
		return
	case req.HeadSHA != "" && !commitSHARe.MatchString(req.HeadSHA):
		apiError(c, http.StatusBadRequest, "head_sha must be a 40-char lowercase hex commit SHA")
		return
	}

	id, _, err := a.evals.enqueue(evaluationRow{
		CampaignID: req.CampaignID, RepositoryFullName: req.Repository, PRNumber: req.PRNumber,
		Event: req.Event, HeadSHA: strPtr(req.HeadSHA), Trigger: "manual",
	})
	if err != nil {
		internalError(c, "queue evaluation", err)
		return
	}
	row, err := a.store.getEvaluation(id)
	if err != nil || row == nil {
		internalError(c, "load evaluation", err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"data": row})
}

// GET /api/evaluations
func (a *evaluationsAPI) list(c *gin.Context) {
	page, err := queryInt(c, "page", 0, 0, 1<<20)
	if err != nil {
		apiError(c, http.StatusBadRequest, err.Error())
		return
	}
	pageSize, err := queryInt(c, "page_size", defaultPageSize, 1, maxPageSize)
	if err != nil {
		apiError(c, http.StatusBadRequest, err.Error())
		return
	}
	prNumber, err := queryInt(c, "pr_number", 0, 1, 1<<30)
	if err != nil {
		apiError(c, http.StatusBadRequest, err.Error())
		return
	}
	f := evaluationListFilter{
		CampaignID: c.Query("campaign_id"), Repo: c.Query("repo"), Status: c.Query("status"),
		PRNumber: prNumber, Page: page, PageSize: pageSize,
	}
	switch {
	case f.CampaignID != "" && !uuidRe.MatchString(f.CampaignID):
		apiError(c, http.StatusBadRequest, "campaign_id must be a campaign UUID")
		return
	case f.Repo != "" && !repoFullNameRe.MatchString(f.Repo):
		apiError(c, http.StatusBadRequest, "repo must be owner/repo")
		return
	case f.Status != "" && f.Status != "pending" && f.Status != "running" && f.Status != "done" && f.Status != "failed":
		apiError(c, http.StatusBadRequest, "status must be one of pending, running, done, failed")
		return
	}

	rows, total, err := a.store.listEvaluations(f)
	if isRangeError(err) {
		rows, err = nil, nil // Page past the end: empty page, like the other list endpoints.
	}
	if err != nil {
		internalError(c, "list evaluations", err)
		return
	}
	if rows == nil {
		rows = []map[string]json.RawMessage{}
	}
	c.JSON(http.StatusOK, gin.H{
		"data": rows,
		"pagination": gin.H{
			"page": page, "page_size": pageSize, "total": total,
			"has_more": int64((page+1)*pageSize) < total,
		},
	})
}

// GET /api/evaluations/:id
func (a *evaluationsAPI) get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		apiError(c, http.StatusNotFound, "evaluation not found")
		return
	}
	row, err := a.store.getEvaluation(id)
	if err != nil {
		internalError(c, "load evaluation", err)
		return
	}
	if row == nil {
		apiError(c, http.StatusNotFound, "evaluation not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": row})
}
