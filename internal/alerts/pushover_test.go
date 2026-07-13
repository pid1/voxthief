package alerts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient returns a client pointed at srv with an instant, recording
// sleep so backoff timing is asserted without real waits.
func newTestClient(srv *httptest.Server) (*Client, *[]time.Duration) {
	var slept []time.Duration
	c := NewClient("tok-secret", "usr-secret")
	c.BaseURL = srv.URL
	c.sleep = func(d time.Duration) { slept = append(slept, d) }
	return c, &slept
}

func TestSendSuccess(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = r.ParseForm()
		if r.Form.Get("token") != "tok-secret" {
			t.Errorf("token not sent")
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":1,"request":"x"}`))
	}))
	defer srv.Close()

	c, _ := newTestClient(srv)
	status, err := c.Send(context.Background(), Message{Message: "hi", Title: "t"})
	if err != nil || status != 200 {
		t.Fatalf("Send: status=%d err=%v", status, err)
	}
	if hits != 1 {
		t.Errorf("expected 1 hit, got %d", hits)
	}
}

func TestSend4xxNoRetry(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"status":0,"errors":["user key is invalid"]}`))
	}))
	defer srv.Close()

	c, slept := newTestClient(srv)
	status, err := c.Send(context.Background(), Message{Message: "hi"})
	if err == nil {
		t.Fatal("expected error on 4xx")
	}
	if status != 400 {
		t.Errorf("status = %d, want 400", status)
	}
	if !strings.Contains(err.Error(), "user key is invalid") {
		t.Errorf("errors array not captured: %v", err)
	}
	if hits != 1 {
		t.Errorf("4xx must not retry; got %d hits", hits)
	}
	if len(*slept) != 0 {
		t.Errorf("no backoff should occur on 4xx, slept %v", *slept)
	}
}

func TestSend5xxRetriesWithBackoff(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"status":0,"errors":["server error"]}`))
	}))
	defer srv.Close()

	c, slept := newTestClient(srv)
	_, err := c.Send(context.Background(), Message{Message: "hi"})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if hits != 4 { // initial + 3 retries
		t.Errorf("expected 4 attempts, got %d", hits)
	}
	want := []time.Duration{time.Second, 5 * time.Second, 25 * time.Second}
	if len(*slept) != 3 || (*slept)[0] != want[0] || (*slept)[1] != want[1] || (*slept)[2] != want[2] {
		t.Errorf("backoff = %v, want %v", *slept, want)
	}
}

func TestSendNetworkErrorRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // force connection errors

	c := NewClient("t", "u")
	c.BaseURL = url
	var slept int
	c.sleep = func(time.Duration) { slept++ }
	_, err := c.Send(context.Background(), Message{Message: "hi"})
	if err == nil {
		t.Fatal("expected network error")
	}
	if slept != 3 {
		t.Errorf("expected 3 backoff sleeps on network errors, got %d", slept)
	}
}

func TestMessageTruncationAndTimestamp(t *testing.T) {
	air := time.Unix(1_700_000_000, 0)
	var gotMsg, gotTs string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotMsg = r.Form.Get("message")
		gotTs = r.Form.Get("timestamp")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
	defer srv.Close()

	c, _ := newTestClient(srv)
	long := strings.Repeat("a", 2000)
	if _, err := c.Send(context.Background(), Message{Message: long, Timestamp: air}); err != nil {
		t.Fatal(err)
	}
	if len([]rune(gotMsg)) != messageMax {
		t.Errorf("message len = %d, want %d", len([]rune(gotMsg)), messageMax)
	}
	if !strings.HasSuffix(gotMsg, "…") {
		t.Errorf("truncated message should end with ellipsis")
	}
	if gotTs != "1700000000" {
		t.Errorf("timestamp = %q, want air time", gotTs)
	}
}

func TestPriority2IncludesRetryExpire(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.Form
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
	defer srv.Close()

	c, _ := newTestClient(srv)
	if _, err := c.Send(context.Background(), Message{Message: "hi", Priority: 2, Retry: 60, Expire: 3600}); err != nil {
		t.Fatal(err)
	}
	if form.Get("priority") != "2" || form.Get("retry") != "60" || form.Get("expire") != "3600" {
		t.Errorf("priority-2 fields missing: %v", form)
	}
}

func TestValidate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if strings.HasSuffix(r.URL.Path, "validate.json") && r.Form.Get("user") == "good" {
			_, _ = w.Write([]byte(`{"status":1}`))
			return
		}
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"status":0,"errors":["user identifier is invalid"]}`))
	}))
	defer srv.Close()

	good := NewClient("t", "good")
	good.BaseURL = srv.URL
	if err := good.Validate(context.Background()); err != nil {
		t.Errorf("valid creds: %v", err)
	}
	bad := NewClient("t", "bad")
	bad.BaseURL = srv.URL
	if err := bad.Validate(context.Background()); err == nil {
		t.Error("expected invalid-credential error")
	}
}

func TestSecretRedaction(t *testing.T) {
	form := url.Values{}
	form.Set("token", "SUPERSECRETTOKEN")
	form.Set("user", "SUPERSECRETUSER")
	form.Set("message", "public message")
	out := SafeForLog(form)
	if strings.Contains(out, "SUPERSECRETTOKEN") || strings.Contains(out, "SUPERSECRETUSER") {
		t.Errorf("secrets leaked in log output: %s", out)
	}
	if !strings.Contains(out, "public message") {
		t.Errorf("non-secret fields should remain: %s", out)
	}
	if redact("x") == "x" || redact("") != "" {
		t.Errorf("redact behavior wrong")
	}
}
