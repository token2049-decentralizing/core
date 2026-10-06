package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	postgrest "github.com/supabase-community/postgrest-go"
	"github.com/supabase-community/supabase-go"
)

const (
	defaultPageSize  = 20
	maxPageSize      = 100
	maxCampaignRepos = 50
)

var repoFullNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// eventListColumns pulls list metadata out of the payload via PostgREST JSON paths
// so list responses don't ship whole payloads.
const eventListColumns = "id,delivery_id,event,action,number,received_at,sender_login," +
	"sender_avatar_url:payload->sender->>avatar_url," +
	"pr_title:payload->pull_request->>title," +
	"pr_state:payload->pull_request->>state," +
	"pr_url:payload->pull_request->>html_url," +
	"pr_merged:payload->pull_request->merged," +
	"issue_title:payload->issue->>title," +
	"issue_state:payload->issue->>state," +
	"issue_url:payload->issue->>html_url," +
	"issue_pull_request:payload->issue->pull_request," +
	"ref:payload->>ref"

type api struct {
	db *supabase.Client
}

func registerAPI(r *gin.Engine, db *supabase.Client) {
	a := &api{db: db}
	g := r.Group("/api")
	g.GET("/repos", a.listRepos)
	g.GET("/repos/:owner/:repo/filters", a.repoFilters)
	g.GET("/repos/:owner/:repo/events", a.listRepoEvents)
	g.GET("/deliveries/:delivery_id", a.getDelivery)
	g.POST("/campaigns", a.createCampaign)
}

func apiError(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"error": msg})
}

func internalError(c *gin.Context, what string, err error) {
	log.Printf("api: %s: %v", what, err)
	apiError(c, http.StatusInternalServerError, "failed to "+what)
}

// GET /api/repos

type repoSummary struct {
	RepositoryFullName string `json:"repository_full_name"`
	EventCount         int64  `json:"event_count"`
	LastEventAt        string `json:"last_event_at"`
}

func (a *api) listRepos(c *gin.Context) {
	var repos []repoSummary
	_, err := a.db.From("github_webhook_repos").
		Select("*", "", false).
		Order("last_event_at", &postgrest.OrderOpts{Ascending: false}).
		ExecuteTo(&repos)
	if err != nil {
		internalError(c, "load repos", err)
		return
	}
	if repos == nil {
		repos = []repoSummary{}
	}
	c.JSON(http.StatusOK, gin.H{"data": repos})
}

// GET /api/repos/:owner/:repo/filters

type facetRow struct {
	Event           string  `json:"event"`
	Action          *string `json:"action"`
	SenderLogin     *string `json:"sender_login"`
	SenderAvatarURL *string `json:"sender_avatar_url"`
	EventCount      int64   `json:"event_count"`
}

type facetValue struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

type eventFacet struct {
	Value   string       `json:"value"`
	Count   int64        `json:"count"`
	Actions []facetValue `json:"actions"`
}

type senderFacet struct {
	Login     string  `json:"login"`
	AvatarURL *string `json:"avatar_url"`
	Count     int64   `json:"count"`
}

func (a *api) repoFilters(c *gin.Context) {
	repo := c.Param("owner") + "/" + c.Param("repo")

	var rows []facetRow
	_, err := a.db.From("github_webhook_repo_facets").
		Select("event,action,sender_login,sender_avatar_url,event_count", "", false).
		Eq("repository_full_name", repo).
		ExecuteTo(&rows)
	if err != nil {
		internalError(c, "load filters", err)
		return
	}
	if len(rows) == 0 {
		apiError(c, http.StatusNotFound, "repository not found")
		return
	}

	var total int64
	events := map[string]*eventFacet{}
	eventActions := map[string]map[string]int64{}
	actions := map[string]int64{}
	senders := map[string]*senderFacet{}
	for _, r := range rows {
		total += r.EventCount
		ef, ok := events[r.Event]
		if !ok {
			ef = &eventFacet{Value: r.Event}
			events[r.Event] = ef
			eventActions[r.Event] = map[string]int64{}
		}
		ef.Count += r.EventCount
		if r.Action != nil {
			eventActions[r.Event][*r.Action] += r.EventCount
			actions[*r.Action] += r.EventCount
		}
		if r.SenderLogin != nil {
			sf, ok := senders[*r.SenderLogin]
			if !ok {
				sf = &senderFacet{Login: *r.SenderLogin, AvatarURL: r.SenderAvatarURL}
				senders[*r.SenderLogin] = sf
			}
			sf.Count += r.EventCount
		}
	}

	eventList := make([]eventFacet, 0, len(events))
	for name, ef := range events {
		ef.Actions = sortedFacets(eventActions[name])
		eventList = append(eventList, *ef)
	}
	sort.Slice(eventList, func(i, j int) bool {
		if eventList[i].Count != eventList[j].Count {
			return eventList[i].Count > eventList[j].Count
		}
		return eventList[i].Value < eventList[j].Value
	})

	senderList := make([]senderFacet, 0, len(senders))
	for _, sf := range senders {
		senderList = append(senderList, *sf)
	}
	sort.Slice(senderList, func(i, j int) bool {
		if senderList[i].Count != senderList[j].Count {
			return senderList[i].Count > senderList[j].Count
		}
		return senderList[i].Login < senderList[j].Login
	})

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"repository_full_name": repo,
		"total_events":         total,
		"events":               eventList,
		"actions":              sortedFacets(actions),
		"senders":              senderList,
	}})
}

func sortedFacets(m map[string]int64) []facetValue {
	out := make([]facetValue, 0, len(m))
	for v, n := range m {
		out = append(out, facetValue{Value: v, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	return out
}

// GET /api/repos/:owner/:repo/events

type eventListRow struct {
	ID               int64           `json:"id"`
	DeliveryID       string          `json:"delivery_id"`
	Event            string          `json:"event"`
	Action           *string         `json:"action"`
	Number           *int64          `json:"number"`
	ReceivedAt       string          `json:"received_at"`
	SenderLogin      *string         `json:"sender_login"`
	SenderAvatarURL  *string         `json:"sender_avatar_url"`
	PRTitle          *string         `json:"pr_title"`
	PRState          *string         `json:"pr_state"`
	PRURL            *string         `json:"pr_url"`
	PRMerged         *bool           `json:"pr_merged"`
	IssueTitle       *string         `json:"issue_title"`
	IssueState       *string         `json:"issue_state"`
	IssueURL         *string         `json:"issue_url"`
	IssuePullRequest json.RawMessage `json:"issue_pull_request"`
	Ref              *string         `json:"ref"`
}

type eventSender struct {
	Login     string  `json:"login"`
	AvatarURL *string `json:"avatar_url"`
}

// eventSubject is the PR or issue an event is about.
type eventSubject struct {
	Type    string  `json:"type"` // "pull_request" | "issue"
	Number  *int64  `json:"number"`
	Title   *string `json:"title"`
	State   *string `json:"state"`
	HTMLURL *string `json:"html_url"`
	Merged  *bool   `json:"merged,omitempty"`
}

type eventListItem struct {
	ID         int64         `json:"id"`
	DeliveryID string        `json:"delivery_id"`
	Event      string        `json:"event"`
	Action     *string       `json:"action"`
	Number     *int64        `json:"number"`
	Sender     *eventSender  `json:"sender"`
	Subject    *eventSubject `json:"subject"`
	Ref        *string       `json:"ref"`
	ReceivedAt string        `json:"received_at"`
}

func (r eventListRow) toItem() eventListItem {
	item := eventListItem{
		ID:         r.ID,
		DeliveryID: r.DeliveryID,
		Event:      r.Event,
		Action:     r.Action,
		Number:     r.Number,
		Ref:        r.Ref,
		ReceivedAt: r.ReceivedAt,
	}
	if r.SenderLogin != nil {
		item.Sender = &eventSender{Login: *r.SenderLogin, AvatarURL: r.SenderAvatarURL}
	}
	switch {
	case r.PRTitle != nil || r.PRURL != nil:
		item.Subject = &eventSubject{Type: "pull_request", Number: r.Number,
			Title: r.PRTitle, State: r.PRState, HTMLURL: r.PRURL, Merged: r.PRMerged}
	case r.IssueTitle != nil || r.IssueURL != nil:
		// Comments on PRs arrive as issue_comment with issue.pull_request set.
		typ := "issue"
		if len(r.IssuePullRequest) > 0 && !bytes.Equal(r.IssuePullRequest, []byte("null")) {
			typ = "pull_request"
		}
		item.Subject = &eventSubject{Type: typ, Number: r.Number,
			Title: r.IssueTitle, State: r.IssueState, HTMLURL: r.IssueURL}
	}
	return item
}

func queryInt(c *gin.Context, name string, def, min, max int) (int, error) {
	s := c.Query(name)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, min, max)
	}
	return n, nil
}

func (a *api) listRepoEvents(c *gin.Context) {
	repo := c.Param("owner") + "/" + c.Param("repo")

	page, err := queryInt(c, "page", 0, 0, 1_000_000)
	if err != nil {
		apiError(c, http.StatusBadRequest, err.Error())
		return
	}
	pageSize, err := queryInt(c, "page_size", defaultPageSize, 1, maxPageSize)
	if err != nil {
		apiError(c, http.StatusBadRequest, err.Error())
		return
	}
	number, err := queryInt(c, "number", 0, 1, 1<<31-1)
	if err != nil {
		apiError(c, http.StatusBadRequest, err.Error())
		return
	}

	filter := func(columns string, head bool) *postgrest.FilterBuilder {
		q := a.db.From("github_webhook_events").
			Select(columns, "exact", head).
			Eq("repository_full_name", repo)
		if v := c.Query("event"); v != "" {
			q = q.Eq("event", v)
		}
		if v := c.Query("action"); v != "" {
			q = q.Eq("action", v)
		}
		if v := c.Query("sender"); v != "" {
			q = q.Eq("sender_login", v)
		}
		if number > 0 {
			q = q.Eq("number", strconv.Itoa(number))
		}
		return q
	}

	var rows []eventListRow
	from := page * pageSize
	total, err := filter(eventListColumns, false).
		Order("received_at", &postgrest.OrderOpts{Ascending: false}).
		Order("id", &postgrest.OrderOpts{Ascending: false}).
		Range(from, from+pageSize-1, "").
		ExecuteTo(&rows)
	if err != nil && strings.Contains(err.Error(), "PGRST103") {
		// Offset past the last row: PostgREST rejects the range, so return an empty page.
		rows = nil
		_, total, err = filter("id", true).Execute()
	}
	if err != nil {
		internalError(c, "load events", err)
		return
	}

	items := make([]eventListItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, r.toItem())
	}
	c.JSON(http.StatusOK, gin.H{
		"data": items,
		"pagination": gin.H{
			"page":      page,
			"page_size": pageSize,
			"total":     total,
			"has_more":  int64(from+len(rows)) < total,
		},
	})
}

// GET /api/deliveries/:delivery_id

type deliveryRow struct {
	ID                 int64           `json:"id"`
	DeliveryID         string          `json:"delivery_id"`
	Event              string          `json:"event"`
	Action             *string         `json:"action"`
	Number             *int64          `json:"number"`
	HookID             *int64          `json:"hook_id"`
	InstallationID     *int64          `json:"installation_id"`
	RepositoryFullName *string         `json:"repository_full_name"`
	SenderLogin        *string         `json:"sender_login"`
	SignatureValid     *bool           `json:"signature_valid"`
	Headers            json.RawMessage `json:"headers"`
	Payload            json.RawMessage `json:"payload"`
	ReceivedAt         string          `json:"received_at"`
}

func (a *api) getDelivery(c *gin.Context) {
	var rows []deliveryRow
	// A redelivered event shares its delivery_id; return the latest attempt.
	_, err := a.db.From("github_webhook_events").
		Select("*", "", false).
		Eq("delivery_id", c.Param("delivery_id")).
		Order("received_at", &postgrest.OrderOpts{Ascending: false}).
		Limit(1, "").
		ExecuteTo(&rows)
	if err != nil {
		internalError(c, "load delivery", err)
		return
	}
	if len(rows) == 0 {
		apiError(c, http.StatusNotFound, "delivery not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rows[0]})
}

// POST /api/campaigns

type createCampaignRequest struct {
	Name           string          `json:"name"`
	Description    *string         `json:"description"`
	Sponsor        *string         `json:"sponsor"`
	RewardAsset    string          `json:"reward_asset"`
	Budget         json.Number     `json:"budget"`
	MaxRewardPerPR json.Number     `json:"max_reward_per_pr"`
	MinScore       *int            `json:"min_score"`
	Eligibility    json.RawMessage `json:"eligibility"`
	Scoring        json.RawMessage `json:"scoring"`
	Status         string          `json:"status"`
	StartsAt       *time.Time      `json:"starts_at"`
	EndsAt         *time.Time      `json:"ends_at"`
	Repos          []string        `json:"repos"`
}

type campaignInsert struct {
	Name           string          `json:"name"`
	Description    *string         `json:"description,omitempty"`
	Sponsor        *string         `json:"sponsor,omitempty"`
	RewardAsset    string          `json:"reward_asset"`
	Budget         json.Number     `json:"budget"`
	MaxRewardPerPR json.Number     `json:"max_reward_per_pr"`
	MinScore       *int            `json:"min_score,omitempty"`
	Eligibility    json.RawMessage `json:"eligibility,omitempty"`
	Scoring        json.RawMessage `json:"scoring,omitempty"`
	Status         string          `json:"status"`
	StartsAt       *time.Time      `json:"starts_at,omitempty"`
	EndsAt         *time.Time      `json:"ends_at,omitempty"`
}

type campaignRepoInsert struct {
	CampaignID         string `json:"campaign_id"`
	RepositoryFullName string `json:"repository_full_name"`
}

// jsonObject normalizes an optional JSON object field: absent/null becomes nil.
func jsonObject(name string, raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if raw[0] != '{' {
		return nil, fmt.Errorf("%s must be a JSON object", name)
	}
	return raw, nil
}

func positiveAmount(name string, n json.Number) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(string(n))
	if !ok || r.Sign() <= 0 {
		return nil, fmt.Errorf("%s must be a positive number", name)
	}
	return r, nil
}

func (req *createCampaignRequest) validate() (*campaignInsert, []string, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return nil, nil, errors.New("name is required")
	}
	if req.RewardAsset == "" {
		req.RewardAsset = "USDC"
	}
	if req.RewardAsset != "USDC" && req.RewardAsset != "SOL" {
		return nil, nil, errors.New(`reward_asset must be "USDC" or "SOL"`)
	}
	budget, err := positiveAmount("budget", req.Budget)
	if err != nil {
		return nil, nil, err
	}
	maxReward, err := positiveAmount("max_reward_per_pr", req.MaxRewardPerPR)
	if err != nil {
		return nil, nil, err
	}
	if maxReward.Cmp(budget) > 0 {
		return nil, nil, errors.New("max_reward_per_pr must not exceed budget")
	}
	if req.MinScore != nil && (*req.MinScore < 0 || *req.MinScore > 100) {
		return nil, nil, errors.New("min_score must be between 0 and 100")
	}
	eligibility, err := jsonObject("eligibility", req.Eligibility)
	if err != nil {
		return nil, nil, err
	}
	scoring, err := jsonObject("scoring", req.Scoring)
	if err != nil {
		return nil, nil, err
	}
	if req.Status == "" {
		req.Status = "draft"
	}
	if req.Status != "draft" && req.Status != "active" {
		return nil, nil, errors.New(`status must be "draft" or "active"`)
	}
	if req.StartsAt != nil && req.EndsAt != nil && !req.EndsAt.After(*req.StartsAt) {
		return nil, nil, errors.New("ends_at must be after starts_at")
	}

	if len(req.Repos) > maxCampaignRepos {
		return nil, nil, fmt.Errorf("at most %d repos per campaign", maxCampaignRepos)
	}
	seen := map[string]bool{}
	var repos []string
	for _, r := range req.Repos {
		r = strings.TrimSpace(r)
		if !repoFullNameRe.MatchString(r) {
			return nil, nil, fmt.Errorf("invalid repo %q, expected owner/repo", r)
		}
		if !seen[r] {
			seen[r] = true
			repos = append(repos, r)
		}
	}

	return &campaignInsert{
		Name:           req.Name,
		Description:    req.Description,
		Sponsor:        req.Sponsor,
		RewardAsset:    req.RewardAsset,
		Budget:         req.Budget,
		MaxRewardPerPR: req.MaxRewardPerPR,
		MinScore:       req.MinScore,
		Eligibility:    eligibility,
		Scoring:        scoring,
		Status:         req.Status,
		StartsAt:       req.StartsAt,
		EndsAt:         req.EndsAt,
	}, repos, nil
}

func (a *api) createCampaign(c *gin.Context) {
	var req createCampaignRequest
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		apiError(c, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	campaign, repos, err := req.validate()
	if err != nil {
		apiError(c, http.StatusBadRequest, err.Error())
		return
	}

	var created []map[string]json.RawMessage
	_, err = a.db.From("campaigns").
		Insert(campaign, false, "", "representation", "").
		ExecuteTo(&created)
	if err != nil || len(created) != 1 {
		internalError(c, "create campaign", err)
		return
	}
	var id string
	if err := json.Unmarshal(created[0]["id"], &id); err != nil {
		internalError(c, "create campaign", err)
		return
	}

	if len(repos) > 0 {
		rows := make([]campaignRepoInsert, len(repos))
		for i, r := range repos {
			rows[i] = campaignRepoInsert{CampaignID: id, RepositoryFullName: r}
		}
		if _, _, err := a.db.From("campaign_repos").
			Insert(rows, false, "", "minimal", "").
			Execute(); err != nil {
			// PostgREST can't span requests in one transaction; roll back by hand.
			if _, _, derr := a.db.From("campaigns").Delete("minimal", "").Eq("id", id).Execute(); derr != nil {
				log.Printf("api: failed to roll back campaign %s: %v", id, derr)
			}
			internalError(c, "attach repos", err)
			return
		}
	}

	if repos == nil {
		repos = []string{}
	}
	reposJSON, _ := json.Marshal(repos)
	created[0]["repos"] = reposJSON
	c.JSON(http.StatusCreated, gin.H{"data": created[0]})
}
