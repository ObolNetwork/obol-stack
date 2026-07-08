package jobbroker

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Contract headers between the x402-verifier and the broker. The verifier
// sets all of them (client-supplied copies never survive the gate), so the
// broker can stay stateless about offers: everything it needs rides the
// request.
const (
	HeaderUpstreamURL  = "X-Obol-Upstream-Url"
	HeaderOffer        = "X-Obol-Offer"
	HeaderPayTo        = "X-Obol-Pay-To"
	HeaderJobTTL       = "X-Obol-Job-Ttl"
	HeaderVisibility   = "X-Obol-Result-Visibility"
	HeaderPublicPrefix = "X-Obol-Public-Prefix"
	HeaderUpstreamAuth = "X-Obol-Upstream-Auth"

	// Set by the verifier from verified credentials.
	HeaderPaymentPayer   = "X-Payment-Payer"
	HeaderVerifiedWallet = "X-Verified-Wallet"
)

// maxSubmitBody caps stored request bodies; results are capped separately.
const (
	maxSubmitBody = 10 << 20 // 10 MiB
	maxResultBody = 64 << 20 // 64 MiB
)

// upstreamTimeout is the broker-side replay budget — generous by design:
// surviving long jobs is the point. Not client-facing.
const upstreamTimeout = 2 * time.Hour

// Server is the broker's HTTP surface.
type Server struct {
	store  *Store
	client *http.Client
	// hmacSecret, when non-empty, requires every submit to carry a valid
	// X-Obol-Broker-Sig computed by the verifier over the contract headers.
	// Empty = disabled (the NetworkPolicy is then the only guard). Sourced
	// from JOB_BROKER_HMAC_SECRET, the same value the verifier signs with.
	hmacSecret string
	// now is swappable for tests.
	now func() time.Time
}

// HeaderBrokerSig carries the verifier's HMAC over the contract headers, so
// the broker can reject forged submits even from a pod the NetworkPolicy
// allows (defense in depth with the F1 NetworkPolicy). Mirrored in
// internal/x402 (see authgate.go) — the two packages don't share code.
const HeaderBrokerSig = "X-Obol-Broker-Sig"

// brokerSignature is the HMAC the verifier and broker both compute over the
// security-critical contract fields: the replay URL, the offer identity, and
// the injected upstream credential. Keep byte-identical to the x402 copy.
func brokerSignature(secret, upstreamURL, offer, upstreamAuth string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(upstreamURL + "\n" + offer + "\n" + upstreamAuth))
	return hex.EncodeToString(mac.Sum(nil))
}

func NewServer(store *Store) *Server {
	secret := os.Getenv("JOB_BROKER_HMAC_SECRET")
	if secret == "" {
		log.Printf("job-broker: JOB_BROKER_HMAC_SECRET unset — verifier→broker HMAC disabled; relying on the NetworkPolicy alone")
	}
	return &Server{
		store:      store,
		client:     &http.Client{Timeout: upstreamTimeout},
		hmacSecret: secret,
		now:        time.Now,
	}
}

// Handler returns the broker mux. The verifier strips the offer prefix
// before proxying, so paths arrive offer-relative:
//
//	/jobs                → list (wallet-scoped)
//	/jobs/<id>           → status (free; JSON or HTML by Accept)
//	/jobs/<id>/result    → stored result (visibility-gated)
//	anything else        → submit (a paid request the verifier settled)
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/jobs":
			s.handleList(w, r)
		case strings.HasPrefix(r.URL.Path, "/jobs/"):
			rest := strings.TrimPrefix(r.URL.Path, "/jobs/")
			id, tail, _ := strings.Cut(rest, "/")
			switch tail {
			case "":
				s.handleStatus(w, r, id)
			case "result":
				s.handleResult(w, r, id)
			default:
				http.NotFound(w, r)
			}
		default:
			s.handleSubmit(w, r)
		}
	})
	return mux
}

// RunSweeper deletes expired jobs every interval until stop is closed.
func (s *Server) RunSweeper(interval time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if n, err := s.store.SweepExpired(s.now()); err != nil {
				log.Printf("job-broker: sweep: %v", err)
			} else if n > 0 {
				log.Printf("job-broker: swept %d expired job(s)", n)
			}
		}
	}
}

// submitBody is the buyer-controlled envelope fields we read from JSON
// submits. The whole body is replayed verbatim to the upstream either way.
type submitBody struct {
	CallbackURL string `json:"callbackUrl"`
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	upstream := r.Header.Get(HeaderUpstreamURL)
	offer := r.Header.Get(HeaderOffer)
	if upstream == "" || offer == "" {
		// Only the verifier sets these — a request without them didn't
		// come through the payment gate.
		http.Error(w, "not a gated async submit (missing broker contract headers)", http.StatusBadRequest)
		return
	}
	// Defense in depth over the NetworkPolicy: when a shared secret is
	// provisioned, header *presence* is not enough — the verifier's HMAC
	// over (upstreamURL, offer, upstreamAuth) must check out, so a pod that
	// slips past the network layer still can't forge an arbitrary-URL,
	// attacker-credentialed submit.
	if s.hmacSecret != "" {
		want := brokerSignature(s.hmacSecret, upstream, offer, r.Header.Get(HeaderUpstreamAuth))
		if !hmac.Equal([]byte(r.Header.Get(HeaderBrokerSig)), []byte(want)) {
			http.Error(w, "invalid or missing broker signature", http.StatusForbidden)
			return
		}
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxSubmitBody+1))
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}
	if len(body) > maxSubmitBody {
		http.Error(w, fmt.Sprintf("request body exceeds the %d-byte async limit", maxSubmitBody), http.StatusRequestEntityTooLarge)
		return
	}

	ttl := monetizeDefaultTTL
	if v := r.Header.Get(HeaderJobTTL); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			ttl = d
		}
	}
	var callback string
	if strings.Contains(r.Header.Get("Content-Type"), "json") {
		var sb submitBody
		if json.Unmarshal(body, &sb) == nil {
			callback = sb.CallbackURL
		}
	}

	now := s.now()
	job := &Job{
		ID:          NewID(),
		Token:       NewID(),
		Offer:       offer,
		Payer:       strings.ToLower(r.Header.Get(HeaderPaymentPayer)),
		PayTo:       r.Header.Get(HeaderPayTo),
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
		Method:      r.Method,
		Path:        r.URL.Path,
		ContentType: r.Header.Get("Content-Type"),
		Body:        body,
		UpstreamURL: upstream,
		CallbackURL: callback,
		Visibility:  visibilityOrDefault(r.Header.Get(HeaderVisibility)),
	}
	if err := s.store.Create(job); err != nil {
		log.Printf("job-broker: create job for %s: %v", offer, err)
		http.Error(w, "could not accept job", http.StatusInternalServerError)
		return
	}

	go s.run(job.ID, r.Header.Get(HeaderUpstreamAuth))

	prefix := r.Header.Get(HeaderPublicPrefix)
	statusPath := prefix + "/jobs/" + job.ID
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", statusPath)
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jobId":     job.ID,
		"statusUrl": statusPath,
		"resultUrl": statusPath + "/result",
		// Capability fallback so pure-API buyers never need a second
		// signature: `Authorization: Bearer <jobToken>` on the result URL.
		"jobToken":  job.Token,
		"expiresAt": job.ExpiresAt.UTC().Format(time.RFC3339),
		"message":   "Job accepted; payment settled. Poll statusUrl (free) until state=complete, then fetch resultUrl.",
	})
}

// run replays the stored request against the upstream and records the
// outcome. No client-facing deadline — that's the point.
func (s *Server) run(id, upstreamAuth string) {
	job, err := s.store.Get(id)
	if err != nil {
		log.Printf("job-broker: load job %s: %v", id, err)
		return
	}
	_ = s.store.MarkRunning(id, s.now())

	url := strings.TrimSuffix(job.UpstreamURL, "/") + job.Path
	req, err := http.NewRequest(job.Method, url, bytes.NewReader(job.Body))
	if err != nil {
		_ = s.store.Finish(id, 0, "", nil, "build upstream request: "+err.Error(), s.now())
		s.fireWebhook(job)
		return
	}
	if job.ContentType != "" {
		req.Header.Set("Content-Type", job.ContentType)
	}
	if upstreamAuth != "" {
		req.Header.Set("Authorization", upstreamAuth)
	}
	req.Header.Set(HeaderPaymentPayer, job.Payer)

	resp, err := s.client.Do(req)
	if err != nil {
		_ = s.store.Finish(id, 0, "", nil, "upstream: "+err.Error(), s.now())
		s.fireWebhook(job)
		return
	}
	defer resp.Body.Close()
	result, err := io.ReadAll(io.LimitReader(resp.Body, maxResultBody))
	if err != nil {
		_ = s.store.Finish(id, resp.StatusCode, "", nil, "read upstream response: "+err.Error(), s.now())
		s.fireWebhook(job)
		return
	}
	_ = s.store.Finish(id, resp.StatusCode, resp.Header.Get("Content-Type"), result, "", s.now())
	s.fireWebhook(job)
}

// fireWebhook POSTs the completion notice with bounded retries.
func (s *Server) fireWebhook(job *Job) {
	if job.CallbackURL == "" {
		return
	}
	fresh, err := s.store.Get(job.ID)
	if err != nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"jobId":     fresh.ID,
		"state":     fresh.State,
		"statusUrl": "/jobs/" + fresh.ID,
		"resultUrl": "/jobs/" + fresh.ID + "/result",
	})
	client := &http.Client{Timeout: 15 * time.Second}
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := client.Post(job.CallbackURL, "application/json", bytes.NewReader(payload))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 400 {
				return
			}
		}
		time.Sleep(time.Duration(attempt+1) * 5 * time.Second)
	}
	log.Printf("job-broker: webhook to %s for job %s gave up after 3 attempts", job.CallbackURL, job.ID)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request, id string) {
	job, err := s.store.Get(id)
	if err != nil {
		writeJobError(w, r, http.StatusNotFound, "No such job", "This job does not exist — or its retention window elapsed and it was deleted.")
		return
	}
	if s.now().After(job.ExpiresAt) {
		writeJobError(w, r, http.StatusGone, "Job expired", "This job's retention window elapsed; the record and result were deleted.")
		return
	}

	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		s.renderStatusHTML(w, job)
		return
	}
	resp := map[string]any{
		"jobId":     job.ID,
		"state":     job.State,
		"createdAt": job.CreatedAt.UTC().Format(time.RFC3339),
		"expiresAt": job.ExpiresAt.UTC().Format(time.RFC3339),
	}
	switch job.State {
	case StateComplete:
		resp["completedAt"] = job.DoneAt.UTC().Format(time.RFC3339)
		resp["resultUrl"] = "/jobs/" + job.ID + "/result"
	case StateFailed:
		resp["completedAt"] = job.DoneAt.UTC().Format(time.RFC3339)
		resp["error"] = jobFailureSummary(job)
	}
	// Prefer: redirect → 303 to the result once complete (delivery-style
	// clients); default JSON keeps pollers simple.
	if job.State == StateComplete && strings.Contains(r.Header.Get("Prefer"), "redirect") {
		http.Redirect(w, r, "/jobs/"+job.ID+"/result", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleResult(w http.ResponseWriter, r *http.Request, id string) {
	job, err := s.store.Get(id)
	if err != nil {
		writeJobError(w, r, http.StatusNotFound, "No such job", "This job does not exist — or its retention window elapsed.")
		return
	}
	if s.now().After(job.ExpiresAt) {
		writeJobError(w, r, http.StatusGone, "Job expired", "This job's retention window elapsed; the result was deleted.")
		return
	}

	if job.Visibility != "public" && !s.callerMayRead(r, job) {
		// The verifier's SIWX layer already 401s invalid credentials on
		// this path when they're SIWX-shaped; here we refuse wallets that
		// aren't the payer and tokens that don't match.
		w.Header().Set("WWW-Authenticate", `Bearer realm="job-result"`)
		writeJobError(w, r, http.StatusUnauthorized, "Not your job",
			"Results are visible to the wallet that paid (sign in with it) or to the bearer of the jobToken from the 202 response.")
		return
	}

	switch job.State {
	case StatePending, StateRunning:
		w.Header().Set("Retry-After", "5")
		writeJobError(w, r, http.StatusConflict, "Not finished yet", "The job is still running. Poll the status URL; the result appears here when state=complete.")
		return
	case StateFailed:
		writeJobError(w, r, http.StatusBadGateway, "Job failed", jobFailureSummary(job)+
			" Payment settles at acceptance, so this run was paid for — report it to the operator contact in /openapi.json (info.contact).")
		return
	}

	ct := job.ResultContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(job.Result)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	wallet := strings.ToLower(r.Header.Get(HeaderVerifiedWallet))
	offer := r.Header.Get(HeaderOffer)
	if wallet == "" {
		w.Header().Set("WWW-Authenticate", `SIWX realm="jobs"`)
		writeJobError(w, r, http.StatusUnauthorized, "Sign in to list jobs",
			"Job listings are wallet-scoped: buyers see the jobs they paid for; the offer's payTo wallet sees all of them. Authenticate via SIWX.")
		return
	}
	items, err := s.store.ListForWallet(offer, wallet, 100)
	if err != nil {
		http.Error(w, "list jobs", http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []ListSummary{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"wallet": wallet, "jobs": items})
}

// callerMayRead: payer-visibility results open to the paying wallet (SIWX,
// header set by the verifier) or the capability token from the 202 body.
func (s *Server) callerMayRead(r *http.Request, job *Job) bool {
	if wallet := strings.ToLower(r.Header.Get(HeaderVerifiedWallet)); wallet != "" && wallet == job.Payer {
		return true
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		if token, ok := strings.CutPrefix(auth, "Bearer "); ok && strings.TrimSpace(token) == job.Token {
			return true
		}
	}
	return false
}

func visibilityOrDefault(v string) string {
	if v == "public" {
		return "public"
	}
	return "payer"
}

func jobFailureSummary(job *Job) string {
	if job.Error != "" {
		return job.Error
	}
	return fmt.Sprintf("upstream returned HTTP %d", job.ResultStatus)
}

const monetizeDefaultTTL = 72 * time.Hour

func writeJobError(w http.ResponseWriter, r *http.Request, status int, title, detail string) {
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_ = statusPageTmpl.Execute(w, statusPageData{
			Status: fmt.Sprintf("%d", status), Title: title, Detail: detail,
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": title, "detail": detail})
}

type statusPageData struct {
	Status  string
	Title   string
	Detail  string
	Job     *Job
	Refresh bool
}

var statusPageTmpl = template.Must(template.New("job_status").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width,initial-scale=1" />
    <title>{{if .Job}}Job {{.Job.State}}{{else}}{{.Status}} — {{.Title}}{{end}}</title>
    <meta name="robots" content="noindex" />
    <meta name="theme-color" content="#091011" />
    {{if .Refresh}}<meta http-equiv="refresh" content="5" />{{end}}
    <style>
      :root { --bg01:#091011; --bg02:#111f22; --stroke:#1e3a3f; --green:#2fe4ab; --light:#d9eef3; --body:#9cc2c9; --muted:#475e64; --red:#ff7a7a; --mono:"JetBrains Mono",ui-monospace,monospace; }
      * { box-sizing:border-box; } html, body { background:var(--bg01); }
      body { margin:0; color:var(--light); font-family:"DM Sans",system-ui,sans-serif; line-height:1.5; }
      .wrap { max-width:560px; margin:0 auto; padding:80px 24px; }
      .state { font-family:var(--mono); font-size:14px; font-weight:600; color:var(--green); margin-bottom:10px; text-transform:uppercase; }
      .state.failed { color:var(--red); }
      h1 { font-size:24px; margin:0 0 10px; }
      p { color:var(--body); margin:0 0 10px; }
      a { color:var(--green); }
      code { font-family:var(--mono); font-size:0.9em; }
      .fineprint { color:var(--muted); font-size:13px; margin-top:28px; }
    </style>
  </head>
  <body>
    <div class="wrap">
      {{if .Job}}
        <div class="state {{.Job.State}}">{{.Job.State}}</div>
        <h1>Job <code>{{.Job.ID}}</code></h1>
        {{if eq .Job.State "complete"}}
          <p>Done. <a href="/jobs/{{.Job.ID}}/result">Fetch the result</a> — sign in with the wallet that paid, or use the jobToken from the acceptance response.</p>
        {{else if eq .Job.State "failed"}}
          <p>{{.Detail}}</p>
          <p>Payment settles at acceptance, so this run was paid for. Report it via the operator contact in <a href="/openapi.json"><code>/openapi.json</code></a> (<code>info.contact</code>).</p>
        {{else}}
          <p>Still working — this page refreshes every 5 seconds.</p>
        {{end}}
        <p class="fineprint">Record expires {{.Job.ExpiresAt.UTC.Format "2006-01-02 15:04 UTC"}} · Powered by Obol Stack</p>
      {{else}}
        <div class="state failed">{{.Status}}</div>
        <h1>{{.Title}}</h1>
        <p>{{.Detail}}</p>
      {{end}}
    </div>
  </body>
</html>
`))

func (s *Server) renderStatusHTML(w http.ResponseWriter, job *Job) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = statusPageTmpl.Execute(w, statusPageData{
		Job:     job,
		Detail:  jobFailureSummary(job),
		Refresh: job.State == StatePending || job.State == StateRunning,
	})
}
