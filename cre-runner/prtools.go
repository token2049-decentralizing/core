package main

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/supabase-community/supabase-go"
)

// PR tools:
//
//	GET  /api/repos/:owner/:repo/prs/:number/status  live PR state from GitHub (prstatus.go)
//	POST /api/executions/:id/rerun                     run an execution again, as a new execution

// rerunner starts one execution for one campaign (executor.Start).
type rerunner interface {
	Start(t *prTrigger, c campaignRow, rerunOf string) (string, error)
}

type prTools struct {
	db    *supabase.Client
	store executionStore
	gh    prGitHub // nil: no GitHub credentials
	runs  rerunner // nil: CRE executions disabled
}

func registerPRTools(r *gin.Engine, t *prTools) {
	r.GET("/api/repos/:owner/:repo/prs/:number/status", t.status)
	r.POST("/api/executions/:id/rerun", t.rerun)
}

func (t *prTools) status(c *gin.Context) {
	repo := c.Param("owner") + "/" + c.Param("repo")
	n, err := strconv.Atoi(c.Param("number"))
	switch {
	case !repoFullNameRe.MatchString(repo):
		apiError(c, http.StatusBadRequest, "repo must be owner/repo")
		return
	case err != nil || n <= 0 || n > 1<<31-1:
		apiError(c, http.StatusBadRequest, "number must be a positive integer")
		return
	case t.gh == nil:
		apiError(c, http.StatusServiceUnavailable, "GitHub credentials are not configured")
		return
	}
	st, err := t.gh.PRStatus(c.Request.Context(), repo, n)
	if err != nil {
		log.Printf("api: pr status %s#%d: %v", repo, n, err)
		apiError(c, http.StatusBadGateway, "couldn't read the pull request from GitHub: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": st})
}

type rerunSource struct {
	ID                 string  `json:"id"`
	DeliveryID         string  `json:"delivery_id"`
	CampaignID         string  `json:"campaign_id"`
	RepositoryFullName string  `json:"repository_full_name"`
	PRNumber           int     `json:"pr_number"`
	Event              string  `json:"event"`
	Status             string  `json:"status"`
	Settled            bool    `json:"settled"`
	HeadSHA            *string `json:"head_sha"`
	AuthorLogin        *string `json:"author_login"`
	AuthorGitHubID     *int64  `json:"author_github_id"`
}

// rerun evaluates the execution's PR again for the same campaign and event, at the PR's
// current head, so edits since then (a "fixes #N" added, new commits) are taken into
// account. The original stays as it was; the new execution records rerun_of.
func (t *prTools) rerun(c *gin.Context) {
	id := c.Param("id")
	if !uuidRe.MatchString(id) {
		apiError(c, http.StatusNotFound, "execution not found")
		return
	}
	if t.runs == nil {
		apiError(c, http.StatusServiceUnavailable, "CRE executions are disabled on this runner")
		return
	}
	var rows []rerunSource
	if _, err := t.db.From(executionsTable).
		Select("id,delivery_id,campaign_id,repository_full_name,pr_number,event,status,settled,head_sha,author_login,author_github_id", "", false).
		Eq("id", id).Limit(1, "").ExecuteTo(&rows); err != nil {
		internalError(c, "load execution", err)
		return
	}
	if len(rows) == 0 {
		apiError(c, http.StatusNotFound, "execution not found")
		return
	}
	src := rows[0]
	switch {
	case src.Status == statusQueued || src.Status == statusRunning:
		apiError(c, http.StatusConflict, "execution is still "+src.Status)
		return
	case src.Event == "merged" && src.Settled:
		apiError(c, http.StatusConflict, "this PR is already settled; a rerun can't pay it again")
		return
	}

	// One evaluation of a PR per campaign at a time (also bounds rerun spam).
	_, inFlight, err := t.db.From(executionsTable).Select("id", "exact", true).
		Eq("campaign_id", src.CampaignID).Eq("repository_full_name", src.RepositoryFullName).
		Eq("pr_number", strconv.Itoa(src.PRNumber)).In("status", []string{statusQueued, statusRunning}).Execute()
	if err != nil {
		internalError(c, "check running executions", err)
		return
	}
	if inFlight > 0 {
		apiError(c, http.StatusConflict, "an execution for this PR is already queued or running")
		return
	}

	campaign, err := t.activeCampaign(src.CampaignID, src.RepositoryFullName)
	if err != nil {
		internalError(c, "load campaign", err)
		return
	}
	if campaign == nil {
		apiError(c, http.StatusConflict, "the campaign is not active for this repository, so it can't be evaluated")
		return
	}

	trigger := &prTrigger{DeliveryID: src.DeliveryID, Repository: src.RepositoryFullName, PRNumber: src.PRNumber, Event: src.Event}
	if src.AuthorLogin != nil && src.AuthorGitHubID != nil {
		trigger.AuthorLogin, trigger.AuthorID = *src.AuthorLogin, *src.AuthorGitHubID
	}
	if t.gh != nil {
		st, err := t.gh.PRStatus(c.Request.Context(), src.RepositoryFullName, src.PRNumber)
		if err != nil {
			apiError(c, http.StatusBadGateway, "couldn't read the pull request from GitHub: "+err.Error())
			return
		}
		if src.Event == "merged" && !st.Merged {
			apiError(c, http.StatusConflict, "the pull request is no longer merged")
			return
		}
		if shaRe.MatchString(st.HeadSHA) {
			trigger.HeadSHA = st.HeadSHA
		}
		if st.AuthorID > 0 {
			trigger.AuthorLogin, trigger.AuthorID = st.AuthorLogin, st.AuthorID
		}
	}

	newID, err := t.runs.Start(trigger, *campaign, src.ID)
	if err != nil {
		internalError(c, "start execution", err)
		return
	}
	log.Printf("api: execution %s rerun as %s", src.ID, newID)
	c.JSON(http.StatusAccepted, gin.H{"data": gin.H{"id": newID, "rerun_of": src.ID}})
}

// activeCampaign returns the campaign if it is active for the repo and within its schedule.
func (t *prTools) activeCampaign(id, repo string) (*campaignRow, error) {
	if t.store == nil {
		return nil, errors.New("no execution store")
	}
	campaigns, err := t.store.ActiveCampaigns(repo)
	if err != nil {
		return nil, err
	}
	for _, c := range campaigns {
		if c.ID == id && c.runningAt(time.Now()) {
			return &c, nil
		}
	}
	return nil, nil
}
