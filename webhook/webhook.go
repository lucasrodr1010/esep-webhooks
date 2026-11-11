// Package webhook holds the logic of the ESEP A8 function: take the JSON that
// GitHub sends when an issue changes, find the issue link, and post
// "Issue Created: <link>" to Slack.
//
// The code is split into small pieces so each can be tested alone:
//
//	ExtractIssueURL  - read the GitHub JSON, return the issue link (pure, no I/O)
//	Payload          - build the JSON body Slack expects (pure)
//	Sender           - "where the message goes": real Slack, or a dry-run printer
//	Handle           - glue: event bytes in, message sent, response text out
//
// Nothing in this package ever prints or logs the Slack webhook URL. That URL
// is a secret (the assignment: pushing it to GitHub costs 2 points).
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SlackURLEnvVar is the environment variable that holds the Slack webhook URL.
// The spec (Part 2, page 4) names it SLACK_URL and says to set it under the
// Lambda's Configuration -> Environment variables, never in code.
const SlackURLEnvVar = "SLACK_URL"

// slackURLFallbackEnvVar is also accepted so the same program can be run
// locally by someone who exported the longer, more descriptive name.
const slackURLFallbackEnvVar = "SLACK_WEBHOOK_URL"

// MessagePrefix is the text the spec requires in front of the link
// (Part 5, page 9: opening an issue posts "Issue Created: <link>").
const MessagePrefix = "Issue Created: "

// ---------------------------------------------------------------------------
// Reading the GitHub event
// ---------------------------------------------------------------------------

// githubEvent is the small slice of GitHub's issue payload we care about.
// GitHub sends much more; encoding/json ignores fields we do not declare.
type githubEvent struct {
	Issue *struct {
		HTMLURL string `json:"html_url"`
	} `json:"issue"`

	// Body is only set if API Gateway "Lambda proxy integration" was left ON
	// by mistake: then GitHub's JSON arrives as a string inside a wrapper.
	// The spec (page 5) says to leave proxy integration OFF, so this is just
	// a safety net.
	Body string `json:"body"`
}

// ExtractIssueURL reads the raw event and returns the issue link.
//
// Return values:
//
//	(url, true,  nil)   an issue event: post this link
//	("",  false, nil)   valid JSON but not an issue event (GitHub's "ping" sent
//	                    when the webhook is created, Postman's "test" string):
//	                    nothing to post, and NOT an error
//	("",  false, err)   not valid JSON, or an issue link that looks wrong
func ExtractIssueURL(event []byte) (string, bool, error) {
	return extractIssueURL(event, 0)
}

func extractIssueURL(event []byte, depth int) (string, bool, error) {
	if !json.Valid(event) {
		return "", false, errors.New("event is not valid JSON")
	}

	var ev githubEvent
	if err := json.Unmarshal(event, &ev); err != nil {
		// Valid JSON of the wrong shape, e.g. the bare string "test".
		// That is not an issue event, so ignore it instead of failing.
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading event: %w", err)
	}

	if ev.Issue == nil || ev.Issue.HTMLURL == "" {
		// Proxy-wrapped event? Look one level down, once.
		if ev.Body != "" && depth == 0 {
			return extractIssueURL([]byte(ev.Body), depth+1)
		}
		return "", false, nil
	}

	link, err := validateIssueURL(ev.Issue.HTMLURL)
	if err != nil {
		return "", false, err
	}
	return link, true, nil
}

// validateIssueURL is a defensive check. The API Gateway URL is public and
// GitHub's webhook secret is left empty (spec, Part 4), so anyone could POST
// anything to it. We only forward https links on github.com, so the Slack
// channel cannot be used to relay arbitrary text or links.
func validateIssueURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("issue.html_url is not a valid URL: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme != "https" || (host != "github.com" && host != "www.github.com") {
		return "", fmt.Errorf("refusing issue.html_url %q: only https://github.com links are forwarded", raw)
	}
	return u.String(), nil
}

// ---------------------------------------------------------------------------
// Building the Slack message
// ---------------------------------------------------------------------------

// Payload returns the JSON body Slack's incoming webhook expects:
// {"text":"Issue Created: <link>"}. json.Marshal handles all escaping.
func Payload(issueURL string) ([]byte, error) {
	return json.Marshal(struct {
		Text string `json:"text"`
	}{Text: MessagePrefix + issueURL})
}

// ---------------------------------------------------------------------------
// Senders: where the message goes
// ---------------------------------------------------------------------------

// Sender delivers one Slack payload and returns the text of the response
// (Slack answers with the plain text "ok" on success).
type Sender interface {
	Send(ctx context.Context, payload []byte) (string, error)
}

// HTTPSender posts to a real incoming-webhook URL (or, in tests, to a local
// fake server standing in for Slack).
type HTTPSender struct {
	url    string
	client *http.Client
}

// NewHTTPSender builds a sender for the given URL. The 8 second timeout keeps
// us under GitHub's 10 second limit for webhook responses (spec, page 9).
// A nil client means "use the default 8 second client".
func NewHTTPSender(webhookURL string, client *http.Client) *HTTPSender {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &HTTPSender{url: webhookURL, client: client}
}

// NewHTTPSenderFromEnv reads the Slack URL from the environment. getenv is
// os.Getenv in real use; tests pass a fake so no real environment is touched.
//
// If the variable is missing or malformed the error says exactly what to fix
// and never contains the value itself.
func NewHTTPSenderFromEnv(getenv func(string) string) (*HTTPSender, error) {
	raw := strings.TrimSpace(getenv(SlackURLEnvVar))
	name := SlackURLEnvVar
	if raw == "" {
		raw = strings.TrimSpace(getenv(slackURLFallbackEnvVar))
		name = slackURLFallbackEnvVar
	}
	if raw == "" {
		return nil, fmt.Errorf("%s is not set: put the Slack webhook URL in the Lambda "+
			"environment variable %s (Configuration -> Environment variables), or export "+
			"it in your shell for a local run; never put it in code or git",
			SlackURLEnvVar, SlackURLEnvVar)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		// Deliberately not printing raw or err: err can quote the value.
		return nil, fmt.Errorf("%s is set but is not an https URL; check the value you pasted", name)
	}
	return NewHTTPSender(raw, nil), nil
}

// Send POSTs the payload and returns Slack's response body.
func (s *HTTPSender) Send(ctx context.Context, payload []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(payload))
	if err != nil {
		return "", errors.New("could not build the Slack request")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		// net/http wraps failures in *url.Error, whose message contains the
		// full URL. That URL is the secret, so keep only the inner cause.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return "", fmt.Errorf("posting to Slack failed: %w", uerr.Err)
		}
		return "", errors.New("posting to Slack failed")
	}
	defer resp.Body.Close()

	// Slack's answers are tiny ("ok", "invalid_payload", ...). Cap the read.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("reading Slack's response: %w", err)
	}
	text := strings.TrimSpace(string(body))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Spec page 11: invalid_payload / no_service / channel_not_found mean
		// the SLACK_URL value is wrong or was revoked. Show Slack's own word.
		return text, fmt.Errorf("slack answered HTTP %d: %q (if this is invalid_payload, "+
			"no_service or channel_not_found, the SLACK_URL value is wrong or revoked)",
			resp.StatusCode, text)
	}
	return text, nil
}

// DryRunSender prints what would be posted and sends nothing anywhere. It
// needs no URL, so it is what you use to demo and test without Slack.
type DryRunSender struct {
	Out io.Writer
}

// Send writes the payload to Out and pretends Slack said "ok".
func (d DryRunSender) Send(_ context.Context, payload []byte) (string, error) {
	fmt.Fprintf(d.Out, "[dry-run] would POST to Slack: %s\n", payload)
	return "ok", nil
}

// ---------------------------------------------------------------------------
// Handle: the whole function in one call
// ---------------------------------------------------------------------------

// Handle is the Lambda handler. API Gateway (proxy integration OFF) passes
// GitHub's JSON as the event. We return the text Slack answered with, which is
// "ok" on success, matching the Postman check on spec page 10.
func Handle(ctx context.Context, event []byte, sender Sender) (string, error) {
	issueURL, isIssue, err := ExtractIssueURL(event)
	if err != nil {
		return "", err
	}
	if !isIssue {
		// GitHub's "ping" lands here when the webhook is first added.
		// Answer 200 so GitHub shows a green tick; post nothing.
		return "ignored: not an issue event", nil
	}

	payload, err := Payload(issueURL)
	if err != nil {
		return "", err
	}
	return sender.Send(ctx, payload)
}
