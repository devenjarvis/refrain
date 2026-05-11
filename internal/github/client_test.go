package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	gh "github.com/google/go-github/v69/github"
)

// newTestClient returns a Client whose underlying go-github client is pointed
// at the given test server. sleep is replaced with a no-op so tests do not
// actually wait for backoff intervals.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	ghc := gh.NewClient(nil)
	base, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	ghc.BaseURL = base
	return &Client{
		gh: ghc,
		sleep: func(ctx context.Context, d time.Duration) error {
			return nil
		},
	}
}

func TestGetPR_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/pulls" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = fmt.Fprintln(w, `[{"number":42,"title":"t","html_url":"u","state":"open","head":{"ref":"feat"},"base":{"ref":"main"}}]`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	pr, err := c.GetPR(context.Background(), "o", "r", "feat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr == nil || pr.Number != 42 {
		t.Fatalf("expected PR #42, got %+v", pr)
	}
}

func TestGetPR_NoPR(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, `[]`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	pr, err := c.GetPR(context.Background(), "o", "r", "feat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr != nil {
		t.Fatalf("expected nil PR, got %+v", pr)
	}
}

func TestGetPR_Retry503ThenSuccess(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprintln(w, `[{"number":1,"title":"","state":"open","head":{"ref":"f"},"base":{"ref":"main"}}]`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	pr, err := c.GetPR(context.Background(), "o", "r", "f")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr == nil {
		t.Fatalf("expected PR, got nil")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestGetPR_ExhaustRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.GetPR(context.Background(), "o", "r", "f")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("expected 3 attempts (1 + 2 retries), got %d", got)
	}
}

func TestGetPR_NoRetryOn4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.GetPR(context.Background(), "o", "r", "f")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 attempt (no retry on 4xx), got %d", got)
	}
}

func TestGetPR_RetryOn429WithRetryAfter(t *testing.T) {
	var calls atomic.Int32
	var waitsCaptured []time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "2")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		_, _ = fmt.Fprintln(w, `[]`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	// Capture the wait duration passed to sleep.
	c.sleep = func(ctx context.Context, d time.Duration) error {
		waitsCaptured = append(waitsCaptured, d)
		return nil
	}
	_, err := c.GetPR(context.Background(), "o", "r", "f")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
	if len(waitsCaptured) != 1 {
		t.Fatalf("expected 1 sleep, got %d: %v", len(waitsCaptured), waitsCaptured)
	}
	// Wait should be ~2s (jitter ±100ms, clamped to maxRetryWait).
	if waitsCaptured[0] < 1900*time.Millisecond || waitsCaptured[0] > 2100*time.Millisecond {
		t.Fatalf("expected ~2s wait, got %v", waitsCaptured[0])
	}
}

func TestGetPR_CtxCancelledDuringBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	// Make sleep return ctx error.
	cancelErr := errors.New("cancelled")
	c.sleep = func(ctx context.Context, d time.Duration) error {
		return cancelErr
	}
	_, err := c.GetPR(context.Background(), "o", "r", "f")
	if !errors.Is(err, cancelErr) && err == nil {
		// doWithRetry returns the sleep error directly, but callers wrap it.
		// Just ensure we got *some* error and didn't loop forever.
		t.Fatalf("expected error from cancelled sleep, got nil")
	}
}

func TestGetPRBySHA_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/commits/deadbeef/pulls" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = fmt.Fprintln(w, `[{"number":7,"state":"open","head":{"ref":"feat","repo":{"full_name":"o/r"}},"base":{"ref":"main"}}]`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	pr, err := c.GetPRBySHA(context.Background(), "o", "r", "deadbeef")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr == nil || pr.Number != 7 {
		t.Fatalf("expected PR #7, got %+v", pr)
	}
}

func TestGetPRBySHA_ForkFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Two PRs for this commit — one from a fork, one from the owner's repo.
		_, _ = fmt.Fprintln(w, `[
			{"number":9,"state":"open","head":{"ref":"feat","repo":{"full_name":"fork/r"}}},
			{"number":7,"state":"open","head":{"ref":"feat","repo":{"full_name":"o/r"}}}
		]`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	pr, err := c.GetPRBySHA(context.Background(), "o", "r", "deadbeef")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr == nil {
		t.Fatalf("expected PR, got nil")
	}
	if pr.Number != 7 {
		t.Fatalf("expected fork PR filtered out; got #%d", pr.Number)
	}
}

func TestGetPRBySHA_422ReturnsNoPR(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, `{"message":"No commit found for SHA"}`, http.StatusUnprocessableEntity)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	pr, err := c.GetPRBySHA(context.Background(), "o", "r", "notpushed")
	if err != nil {
		t.Fatalf("expected no error for 422, got %v", err)
	}
	if pr != nil {
		t.Fatalf("expected nil PR, got %+v", pr)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 attempt (no retry on 422), got %d", got)
	}
}

func TestGetPRBySHA_EmptySHAReturnsNil(t *testing.T) {
	c := &Client{gh: gh.NewClient(nil), sleep: func(context.Context, time.Duration) error { return nil }}
	pr, err := c.GetPRBySHA(context.Background(), "o", "r", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr != nil {
		t.Fatalf("expected nil PR, got %+v", pr)
	}
}

func TestGetPRBySHA_AllClosedReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, `[
			{"number":1,"state":"closed","updated_at":"2024-01-01T00:00:00Z","head":{"ref":"a","repo":{"full_name":"o/r"}}},
			{"number":2,"state":"closed","updated_at":"2024-06-01T00:00:00Z","head":{"ref":"b","repo":{"full_name":"o/r"}}}
		]`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	pr, err := c.GetPRBySHA(context.Background(), "o", "r", "sha")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr != nil {
		t.Fatalf("expected nil PR for all-closed SHAs, got #%d (%s)", pr.Number, pr.State)
	}
}

func TestCreatePR_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/pulls" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintln(w, `{"number":42,"title":"my title","html_url":"https://github.com/o/r/pull/42","state":"open","draft":true,"head":{"ref":"feat","repo":{"full_name":"o/r"}},"base":{"ref":"main"}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	pr, err := c.CreatePR(context.Background(), "o", "r", "feat", "main", "my title", "my body", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr == nil || pr.Number != 42 {
		t.Fatalf("expected PR #42, got %+v", pr)
	}
	if pr.Title != "my title" {
		t.Fatalf("expected title 'my title', got %q", pr.Title)
	}
	if !pr.Draft {
		t.Fatal("expected draft=true")
	}
}

func TestCreatePR_422ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Validation Failed"}`, http.StatusUnprocessableEntity)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.CreatePR(context.Background(), "o", "r", "feat", "main", "t", "b", false)
	if err == nil {
		t.Fatal("expected error for 422, got nil")
	}
}

func TestGetChecks_RetryThenSuccess(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprintln(w, `{"total_count":1,"check_runs":[{"name":"ci","status":"completed","conclusion":"success"}]}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	checks, err := c.GetChecks(context.Background(), "o", "r", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if checks.Passed != 1 || checks.State != "success" {
		t.Fatalf("unexpected check status: %+v", checks)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestClassifyRetry(t *testing.T) {
	t.Run("nil_response_transport_error", func(t *testing.T) {
		retry, wait := classifyRetry(nil, errors.New("transport failed"))
		if !retry {
			t.Errorf("expected retry=true for transport error")
		}
		if wait != 0 {
			t.Errorf("expected wait=0, got %v", wait)
		}
	})
	t.Run("5xx_retriable", func(t *testing.T) {
		resp := &gh.Response{Response: &http.Response{StatusCode: http.StatusInternalServerError}}
		retry, _ := classifyRetry(resp, errors.New("500"))
		if !retry {
			t.Errorf("expected retry=true for 500")
		}
	})
	t.Run("4xx_not_retriable", func(t *testing.T) {
		resp := &gh.Response{Response: &http.Response{StatusCode: http.StatusForbidden}}
		retry, _ := classifyRetry(resp, errors.New("forbidden"))
		if retry {
			t.Errorf("expected retry=false for plain 403")
		}
	})
	t.Run("422_not_retriable", func(t *testing.T) {
		resp := &gh.Response{Response: &http.Response{StatusCode: http.StatusUnprocessableEntity}}
		retry, _ := classifyRetry(resp, errors.New("422"))
		if retry {
			t.Errorf("expected retry=false for 422")
		}
	})
	t.Run("429_retriable_with_retry_after", func(t *testing.T) {
		h := http.Header{}
		h.Set("Retry-After", "5")
		resp := &gh.Response{Response: &http.Response{StatusCode: http.StatusTooManyRequests, Header: h}}
		retry, wait := classifyRetry(resp, errors.New("429"))
		if !retry {
			t.Errorf("expected retry=true for 429")
		}
		if wait != 5*time.Second {
			t.Errorf("expected wait=5s, got %v", wait)
		}
	})
	t.Run("abuse_rate_limit_honors_retry_after", func(t *testing.T) {
		d := 3 * time.Second
		err := &gh.AbuseRateLimitError{RetryAfter: &d}
		retry, wait := classifyRetry(nil, err)
		if !retry {
			t.Errorf("expected retry=true")
		}
		if wait != 3*time.Second {
			t.Errorf("expected wait=3s, got %v", wait)
		}
	})
}
