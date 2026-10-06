package main

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	postgrest "github.com/supabase-community/postgrest-go"
	"github.com/supabase-community/supabase-go"
)

const evaluationsTable = "evaluations"

var errUnexpectedInsert = errors.New("insert returned no row")

// evaluationRow maps to public.evaluations (see migrations/003_evaluations.sql).
type evaluationRow struct {
	ID                 int64   `json:"id,omitempty"`
	CampaignID         string  `json:"campaign_id"`
	RepositoryFullName string  `json:"repository_full_name"`
	PRNumber           int     `json:"pr_number"`
	HeadSHA            *string `json:"head_sha"`
	Event              string  `json:"event"`
	Trigger            string  `json:"trigger"`
	DeliveryID         *string `json:"delivery_id"`
	Status             string  `json:"status"`
}

type evaluationListFilter struct {
	CampaignID, Repo, Status string
	PRNumber                 int
	Page, PageSize           int
}

// evalStore is the persistence evaluations need; supabaseEvalStore in production, a fake in tests.
type evalStore interface {
	activeCampaignsForRepo(repo string) ([]string, error)
	createEvaluation(row evaluationRow) (id int64, created bool, err error)
	updateEvaluation(id int64, fields map[string]any) error
	failInterrupted() error
	listEvaluations(f evaluationListFilter) ([]map[string]json.RawMessage, int64, error)
	getEvaluation(id int64) (map[string]json.RawMessage, error)
}

type supabaseEvalStore struct{ db *supabase.Client }

func (s supabaseEvalStore) activeCampaignsForRepo(repo string) ([]string, error) {
	var rows []struct {
		CampaignID string `json:"campaign_id"`
	}
	_, err := s.db.From("campaign_repos").
		Select("campaign_id,campaigns!inner(status)", "", false).
		Eq("repository_full_name", repo).
		Eq("campaigns.status", "active").
		ExecuteTo(&rows)
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.CampaignID
	}
	return ids, err
}

// isUniqueViolation: Postgres 23505, i.e. a webhook redelivery of an evaluation we already have.
func isUniqueViolation(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "23505") || strings.Contains(err.Error(), "duplicate key"))
}

func (s supabaseEvalStore) createEvaluation(row evaluationRow) (int64, bool, error) {
	var created []struct {
		ID int64 `json:"id"`
	}
	_, err := s.db.From(evaluationsTable).Insert(row, false, "", "representation", "").ExecuteTo(&created)
	switch {
	case isUniqueViolation(err):
		return 0, false, nil
	case err != nil:
		return 0, false, err
	case len(created) != 1:
		return 0, false, errUnexpectedInsert
	}
	return created[0].ID, true, nil
}

func (s supabaseEvalStore) updateEvaluation(id int64, fields map[string]any) error {
	fields["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	_, _, err := s.db.From(evaluationsTable).Update(fields, "minimal", "").Eq("id", strconv.FormatInt(id, 10)).Execute()
	return err
}

// failInterrupted closes rows left in flight by a previous process.
func (s supabaseEvalStore) failInterrupted() error {
	_, _, err := s.db.From(evaluationsTable).
		Update(map[string]any{"status": "failed", "error": "interrupted: cre-runner restarted"}, "minimal", "").
		In("status", []string{"pending", "running"}).
		Execute()
	return err
}

// evaluationColumns embeds the campaign name and reward asset for display.
const evaluationColumns = "*,campaign:campaigns(name,reward_asset)"

func (s supabaseEvalStore) listEvaluations(f evaluationListFilter) ([]map[string]json.RawMessage, int64, error) {
	q := s.db.From(evaluationsTable).Select(evaluationColumns, "exact", false)
	if f.CampaignID != "" {
		q = q.Eq("campaign_id", f.CampaignID)
	}
	if f.Repo != "" {
		q = q.Eq("repository_full_name", f.Repo)
	}
	if f.PRNumber > 0 {
		q = q.Eq("pr_number", strconv.Itoa(f.PRNumber))
	}
	if f.Status != "" {
		q = q.Eq("status", f.Status)
	}
	from := f.Page * f.PageSize
	var rows []map[string]json.RawMessage
	total, err := q.Order("created_at", &postgrest.OrderOpts{Ascending: false}).
		Order("id", &postgrest.OrderOpts{Ascending: false}).
		Range(from, from+f.PageSize-1, "").
		ExecuteTo(&rows)
	return rows, total, err
}

func (s supabaseEvalStore) getEvaluation(id int64) (map[string]json.RawMessage, error) {
	var rows []map[string]json.RawMessage
	_, err := s.db.From(evaluationsTable).Select(evaluationColumns, "", false).
		Eq("id", strconv.FormatInt(id, 10)).Limit(1, "").ExecuteTo(&rows)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}
