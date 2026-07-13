package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the Pushover API root (§7.2). Overridable for tests.
const DefaultBaseURL = "https://api.pushover.net"

// backoff schedule for retryable failures: 1s, 5s, 25s (§7.2).
var backoff = []time.Duration{1 * time.Second, 5 * time.Second, 25 * time.Second}

// Client posts messages to Pushover. Secrets are never logged (§7.2, §12).
type Client struct {
	Token   string
	UserKey string
	BaseURL string
	HTTP    *http.Client
	sleep   func(time.Duration)
}

// NewClient builds a Client with sensible defaults.
func NewClient(token, userKey string) *Client {
	return &Client{
		Token:   token,
		UserKey: userKey,
		BaseURL: DefaultBaseURL,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
		sleep:   time.Sleep,
	}
}

// Message is one notification (§7.2).
type Message struct {
	Message   string
	Title     string
	Timestamp time.Time // transmission air time, not send time (§7.2)
	Priority  int
	Sound     string
	Retry     int // required when Priority == 2
	Expire    int // required when Priority == 2
}

type pushoverResponse struct {
	Status  int      `json:"status"`
	Request string   `json:"request"`
	Errors  []string `json:"errors"`
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) sleepFn() func(time.Duration) {
	if c.sleep != nil {
		return c.sleep
	}
	return time.Sleep
}

// Validate checks the token/user pair via /1/users/validate.json for a crisp
// invalid-credential error (§7.3).
func (c *Client) Validate(ctx context.Context) error {
	form := url.Values{}
	form.Set("token", c.Token)
	form.Set("user", c.UserKey)
	status, resp, err := c.post(ctx, "/1/users/validate.json", form)
	if err != nil {
		return err
	}
	if status != http.StatusOK || resp.Status != 1 {
		return fmt.Errorf("pushover credential validation failed: %s", strings.Join(resp.Errors, "; "))
	}
	return nil
}

// Send delivers m, applying the retry policy of §7.2: 5xx or network error →
// up to 3 attempts with 1s/5s/25s backoff; 4xx is permanent and never retried,
// with Pushover's errors array captured verbatim. Returns the final HTTP status.
func (c *Client) Send(ctx context.Context, m Message) (int, error) {
	form := c.buildForm(m)

	var lastStatus int
	var lastErr error
	attempts := len(backoff) + 1 // initial try + 3 retries
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			c.sleepFn()(backoff[attempt-1])
		}
		status, resp, err := c.post(ctx, "/1/messages.json", form)
		lastStatus = status
		if err != nil {
			lastErr = err // network error: retryable
			continue
		}
		switch {
		case status == http.StatusOK && resp.Status == 1:
			return status, nil
		case status >= 400 && status < 500:
			// Permanent — never retry (§7.2). Capture errors verbatim.
			return status, fmt.Errorf("pushover rejected message (HTTP %d): %s",
				status, strings.Join(resp.Errors, "; "))
		default:
			// 5xx or unexpected: retryable.
			lastErr = fmt.Errorf("pushover HTTP %d: %s", status, strings.Join(resp.Errors, "; "))
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("pushover send failed after %d attempts", attempts)
	}
	return lastStatus, lastErr
}

func (c *Client) buildForm(m Message) url.Values {
	form := url.Values{}
	form.Set("token", c.Token)
	form.Set("user", c.UserKey)
	form.Set("message", truncateEllipsis(m.Message, messageMax))
	if m.Title != "" {
		form.Set("title", m.Title)
	}
	if !m.Timestamp.IsZero() {
		form.Set("timestamp", strconv.FormatInt(m.Timestamp.Unix(), 10))
	}
	if m.Priority != 0 {
		form.Set("priority", strconv.Itoa(m.Priority))
	}
	if m.Sound != "" {
		form.Set("sound", m.Sound)
	}
	if m.Priority == 2 {
		form.Set("retry", strconv.Itoa(m.Retry))
		form.Set("expire", strconv.Itoa(m.Expire))
	}
	return form
}

func (c *Client) post(ctx context.Context, path string, form url.Values) (int, pushoverResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+path, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, pushoverResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, pushoverResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var pr pushoverResponse
	_ = json.Unmarshal(body, &pr)
	return resp.StatusCode, pr, nil
}

// truncateEllipsis shortens s to max runes with a trailing ellipsis (§7.2).
func truncateEllipsis(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// redact masks a secret for logging: shows nothing but its presence (§7.2, §12).
func redact(s string) string {
	if s == "" {
		return ""
	}
	return "***redacted***"
}

// SafeForLog renders a form for --verbose HTTP logging with token/user masked
// (§7.2). Secrets never appear in output.
func SafeForLog(form url.Values) string {
	var b strings.Builder
	for k, vs := range form {
		for _, v := range vs {
			if k == "token" || k == "user" {
				v = redact(v)
			}
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(v)
		}
	}
	return b.String()
}
