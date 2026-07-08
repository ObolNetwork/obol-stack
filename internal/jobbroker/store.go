// Package jobbroker is the stack's async job broker: it accepts paid
// requests the x402-verifier has already verified + settled, replays them
// against the offer's real upstream with no client-facing deadline, and
// serves free status pages plus gated results. One broker per stack,
// multi-tenant across offers; jobs are runtime data in SQLite on a PVC —
// deliberately NOT CRDs (etcd write amplification, 1.5MiB caps on result
// bodies, TTL churn) and NOT k8s Jobs (the work is an HTTP replay against
// a running Service, not a batch pod).
package jobbroker

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Job states.
const (
	StatePending  = "pending"
	StateRunning  = "running"
	StateComplete = "complete"
	StateFailed   = "failed"
)

// Job is one accepted async request and (eventually) its stored result.
type Job struct {
	ID        string
	Token     string // capability credential returned in the 202 body
	Offer     string // "<namespace>/<name>"
	Payer     string // wallet that paid (lowercase 0x…), may be empty
	PayTo     string // offer's payTo wallet (sellers list their jobs with it)
	State     string
	CreatedAt time.Time
	StartedAt time.Time
	DoneAt    time.Time
	ExpiresAt time.Time

	Method      string
	Path        string // path relative to the offer root, e.g. /submit
	ContentType string
	Body        []byte

	UpstreamURL string // in-cluster base URL to replay against
	CallbackURL string // optional completion webhook
	Visibility  string // payer | public

	ResultStatus      int
	ResultContentType string
	Result            []byte
	Error             string
}

// Store persists jobs. All methods are safe for concurrent use (database/sql
// pools; SQLite serializes writers).
type Store struct{ db *sql.DB }

// OpenStore opens (creating if needed) the job database at path. Use
// ":memory:" for tests.
func OpenStore(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		dsn = "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open job store: %w", err)
	}
	// SQLite allows one writer; a single connection avoids SQLITE_BUSY
	// races entirely at broker scale.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS jobs (
	id TEXT PRIMARY KEY,
	token TEXT NOT NULL,
	offer TEXT NOT NULL,
	payer TEXT NOT NULL DEFAULT '',
	pay_to TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	started_at INTEGER NOT NULL DEFAULT 0,
	done_at INTEGER NOT NULL DEFAULT 0,
	expires_at INTEGER NOT NULL,
	method TEXT NOT NULL,
	path TEXT NOT NULL,
	content_type TEXT NOT NULL DEFAULT '',
	body BLOB,
	upstream_url TEXT NOT NULL,
	callback_url TEXT NOT NULL DEFAULT '',
	visibility TEXT NOT NULL DEFAULT 'payer',
	result_status INTEGER NOT NULL DEFAULT 0,
	result_content_type TEXT NOT NULL DEFAULT '',
	result BLOB,
	error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS jobs_offer ON jobs(offer, created_at);
CREATE INDEX IF NOT EXISTS jobs_payer ON jobs(payer, created_at);
CREATE INDEX IF NOT EXISTS jobs_expiry ON jobs(expires_at);
`); err != nil {
		return nil, fmt.Errorf("init job store schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// NewID returns a 128-bit random hex id. Unguessability is load-bearing:
// in public-visibility mode the id IS the result capability.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("jobbroker: crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

func (s *Store) Create(j *Job) error {
	_, err := s.db.Exec(`INSERT INTO jobs
		(id, token, offer, payer, pay_to, state, created_at, expires_at,
		 method, path, content_type, body, upstream_url, callback_url, visibility)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.Token, j.Offer, j.Payer, j.PayTo, StatePending,
		j.CreatedAt.Unix(), j.ExpiresAt.Unix(),
		j.Method, j.Path, j.ContentType, j.Body, j.UpstreamURL, j.CallbackURL, j.Visibility)
	return err
}

func (s *Store) Get(id string) (*Job, error) {
	row := s.db.QueryRow(`SELECT id, token, offer, payer, pay_to, state,
		created_at, started_at, done_at, expires_at,
		method, path, content_type, body, upstream_url, callback_url, visibility,
		result_status, result_content_type, result, error
		FROM jobs WHERE id = ?`, id)
	var j Job
	var created, started, done, expires int64
	err := row.Scan(&j.ID, &j.Token, &j.Offer, &j.Payer, &j.PayTo, &j.State,
		&created, &started, &done, &expires,
		&j.Method, &j.Path, &j.ContentType, &j.Body, &j.UpstreamURL, &j.CallbackURL, &j.Visibility,
		&j.ResultStatus, &j.ResultContentType, &j.Result, &j.Error)
	if err != nil {
		return nil, err
	}
	j.CreatedAt = time.Unix(created, 0)
	j.StartedAt = time.Unix(started, 0)
	j.DoneAt = time.Unix(done, 0)
	j.ExpiresAt = time.Unix(expires, 0)
	return &j, nil
}

func (s *Store) MarkRunning(id string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE jobs SET state=?, started_at=? WHERE id=?`, StateRunning, at.Unix(), id)
	return err
}

// Finish records the upstream outcome. status<400 → complete, else failed
// (money already settled at acceptance — the status page says so plainly).
func (s *Store) Finish(id string, status int, contentType string, body []byte, errMsg string, at time.Time) error {
	state := StateComplete
	if errMsg != "" || status >= 400 {
		state = StateFailed
	}
	_, err := s.db.Exec(`UPDATE jobs SET state=?, done_at=?, result_status=?,
		result_content_type=?, result=?, error=? WHERE id=?`,
		state, at.Unix(), status, contentType, body, errMsg, id)
	return err
}

// ListSummary is the /jobs listing row (no bodies).
type ListSummary struct {
	ID        string    `json:"jobId"`
	State     string    `json:"state"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"createdAt"`
}

// ListForWallet returns jobs the wallet may see under one offer: all of the
// offer's jobs when the wallet is the offer's payTo (the seller), else only
// jobs the wallet paid for.
func (s *Store) ListForWallet(offer, wallet string, limit int) ([]ListSummary, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, state, path, created_at FROM jobs
		WHERE offer = ? AND (LOWER(pay_to) = LOWER(?) OR LOWER(payer) = LOWER(?))
		ORDER BY created_at DESC LIMIT ?`, offer, wallet, wallet, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ListSummary
	for rows.Next() {
		var item ListSummary
		var created int64
		if err := rows.Scan(&item.ID, &item.State, &item.Path, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = time.Unix(created, 0)
		out = append(out, item)
	}
	return out, rows.Err()
}

// SweepExpired deletes jobs past their TTL. Returns rows removed.
func (s *Store) SweepExpired(now time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM jobs WHERE expires_at < ?`, now.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
