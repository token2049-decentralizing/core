package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
)

// ledger makes "merged" evaluations idempotent: the first eligible result per
// (campaign, repo, PR) is recorded and replayed, so a retried webhook never pays twice.
// Stopgap until the Solana program enforces this on-chain.
type ledger struct {
	path string // JSON lines; empty = memory only

	mu      sync.Mutex
	entries map[string]*ledgerEntry
}

type ledgerEntry struct {
	mu  sync.Mutex // Serializes evaluations of the same PR.
	res *evaluationResponse
}

type ledgerRecord struct {
	Key    string              `json:"key"`
	Result *evaluationResponse `json:"result"`
}

func settlementKey(r *evaluationRequest) string {
	return fmt.Sprintf("%s|%s|%d", r.CampaignID, r.Repository, r.PRNumber)
}

func openLedger(path string) (*ledger, error) {
	l := &ledger{path: path, entries: map[string]*ledgerEntry{}}
	if path == "" {
		return l, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec ledgerRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return nil, fmt.Errorf("ledger %s: %w", path, err)
		}
		l.entries[rec.Key] = &ledgerEntry{res: rec.Result}
	}
	return l, sc.Err()
}

// acquire locks the PR's entry. Callers must call release.
func (l *ledger) acquire(key string) *ledgerEntry {
	l.mu.Lock()
	e, ok := l.entries[key]
	if !ok {
		e = &ledgerEntry{}
		l.entries[key] = e
	}
	l.mu.Unlock()
	e.mu.Lock()
	return e
}

func (e *ledgerEntry) release() { e.mu.Unlock() }

// record stores a settled result; caller holds the entry lock.
func (l *ledger) record(key string, e *ledgerEntry, res *evaluationResponse) error {
	if l.path != "" {
		f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		line, _ := json.Marshal(ledgerRecord{Key: key, Result: res})
		_, werr := f.Write(append(line, '\n'))
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
	}
	e.res = res
	return nil
}
