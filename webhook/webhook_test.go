package webhook

// Tests use only Go's standard library. Nothing here touches the internet:
// "Slack" is a fake server on 127.0.0.1 made with net/http/httptest.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const goodURL = "https://github.com/someone/esep-webhooks/issues/7"

// recordingSender is a fake Sender that remembers what it was asked to send.
type recordingSender struct {
	payloads [][]byte
}

func (r *recordingSender) Send(_ context.Context, p []byte) (string, error) {
	r.payloads = append(r.payloads, p)
	return "ok", nil
}

// ---- ExtractIssueURL --------------------------------------------------------

func TestExtractIssueURL(t *testing.T) {
	tests := []struct {
		name    string
		event   string
		wantURL string
		wantOK  bool
		wantErr string // substring; "" means no error expected
	}{
		{"spec's Postman body (page 10)", `{ "issue": { "html_url": "` + goodURL + `" } }`, goodURL, true, ""},
		{"full GitHub opened event", `{"action":"opened","issue":{"html_url":"` + goodURL + `","number":7},"sender":{"login":"x"}}`, goodURL, true, ""},
		{"edited and closed events also carry the link", `{"action":"closed","issue":{"html_url":"` + goodURL + `"}}`, goodURL, true, ""},
		{"GitHub ping has no issue", `{"zen":"Keep it logically awesome.","hook_id":1}`, "", false, ""},
		{"Postman 'test' string (page 6)", `"test"`, "", false, ""},
		{"a number", `42`, "", false, ""},
		{"null", `null`, "", false, ""},
		{"issue with no html_url", `{"issue":{"number":3}}`, "", false, ""},
		{"empty html_url", `{"issue":{"html_url":""}}`, "", false, ""},
		{"not JSON", `{oops`, "", false, "not valid JSON"},
		{"empty input", ``, "", false, "not valid JSON"},
		{"http (not https) link refused", `{"issue":{"html_url":"http://github.com/a/b/issues/1"}}`, "", false, "refusing"},
		{"other host refused", `{"issue":{"html_url":"https://evil.example/a/b/issues/1"}}`, "", false, "refusing"},
		{"lookalike host refused", `{"issue":{"html_url":"https://github.com.evil.example/x"}}`, "", false, "refusing"},
		{"javascript: link refused", `{"issue":{"html_url":"javascript:alert(1)"}}`, "", false, "refusing"},
		{"proxy-wrapped body (integration left on by mistake)",
			`{"body":"{\"issue\":{\"html_url\":\"` + goodURL + `\"}}"}`, goodURL, true, ""},
		{"wrapped body that is not an issue", `{"body":"{\"zen\":\"hi\"}"}`, "", false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotURL, gotOK, err := ExtractIssueURL([]byte(tc.event))
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
			if gotURL != tc.wantURL || gotOK != tc.wantOK {
				t.Errorf("got (%q, %v), want (%q, %v)", gotURL, gotOK, tc.wantURL, tc.wantOK)
			}
		})
	}
}

// A body wrapped inside a body must not recurse forever.
func TestExtractIssueURLDoesNotRecurseDeeply(t *testing.T) {
	inner := `{"body":"{\"issue\":{\"html_url\":\"` + goodURL + `\"}}"}`
	b, _ := json.Marshal(map[string]string{"body": inner})
	if _, ok, err := ExtractIssueURL(b); ok || err != nil {
		t.Errorf("double-wrapped event: ok=%v err=%v, want ignored", ok, err)
	}
}

// ---- Payload -----------------------------------------------------------------

func TestPayloadIsTheExactSpecMessage(t *testing.T) {
	got, err := Payload(goodURL)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"text":"Issue Created: ` + goodURL + `"}`
	if string(got) != want {
		t.Errorf("payload = %s, want %s", got, want)
	}
}

func TestPayloadEscapesAwkwardCharacters(t *testing.T) {
	got, _ := Payload(`https://github.com/a/b/issues/1?x="q"&y=\`)
	var back struct{ Text string }
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("payload is not valid JSON: %v (%s)", err, got)
	}
}

// ---- Handle ------------------------------------------------------------------

func TestHandleSendsOneMessageForAnIssue(t *testing.T) {
	s := &recordingSender{}
	res, err := Handle(context.Background(), []byte(`{"issue":{"html_url":"`+goodURL+`"}}`), s)
	if err != nil || res != "ok" {
		t.Fatalf("res=%q err=%v, want \"ok\", nil", res, err)
	}
	if len(s.payloads) != 1 || string(s.payloads[0]) != `{"text":"Issue Created: `+goodURL+`"}` {
		t.Errorf("payloads = %q", s.payloads)
	}
}

func TestHandlePostsNothingForPingOrTestString(t *testing.T) {
	for _, ev := range []string{`{"zen":"x","hook_id":1}`, `"test"`} {
		s := &recordingSender{}
		res, err := Handle(context.Background(), []byte(ev), s)
		if err != nil {
			t.Errorf("%s: unexpected error %v", ev, err)
		}
		if len(s.payloads) != 0 {
			t.Errorf("%s: posted %q, want nothing", ev, s.payloads)
		}
		if !strings.HasPrefix(res, "ignored") {
			t.Errorf("%s: result %q, want it to start with \"ignored\"", ev, res)
		}
	}
}

func TestHandleReturnsErrorAndPostsNothingForBadInput(t *testing.T) {
	s := &recordingSender{}
	if _, err := Handle(context.Background(), []byte(`{nope`), s); err == nil {
		t.Error("want an error for invalid JSON")
	}
	if _, err := Handle(context.Background(), []byte(`{"issue":{"html_url":"https://evil.example/x"}}`), s); err == nil {
		t.Error("want an error for a non-GitHub link")
	}
	if len(s.payloads) != 0 {
		t.Errorf("posted %q, want nothing", s.payloads)
	}
}

// ---- DryRunSender --------------------------------------------------------------

func TestDryRunSenderPrintsAndSendsNothing(t *testing.T) {
	var out bytes.Buffer
	res, err := Handle(context.Background(), []byte(`{"issue":{"html_url":"`+goodURL+`"}}`), DryRunSender{Out: &out})
	if err != nil || res != "ok" {
		t.Fatalf("res=%q err=%v", res, err)
	}
	if !strings.Contains(out.String(), "[dry-run]") || !strings.Contains(out.String(), "Issue Created: "+goodURL) {
		t.Errorf("dry-run output = %q", out.String())
	}
}

// ---- HTTPSender against a fake Slack ----------------------------------------------

func TestHTTPSenderPostsJSONAndReturnsSlacksAnswer(t *testing.T) {
	var gotMethod, gotType string
	var gotBody []byte
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotType = r.Method, r.Header.Get("Content-Type")
		gotBody = make([]byte, r.ContentLength)
		r.Body.Read(gotBody)
		w.Write([]byte("ok"))
	}))
	defer fake.Close()

	res, err := Handle(context.Background(), []byte(`{"issue":{"html_url":"`+goodURL+`"}}`), NewHTTPSender(fake.URL, nil))
	if err != nil || res != "ok" {
		t.Fatalf("res=%q err=%v", res, err)
	}
	if gotMethod != "POST" || gotType != "application/json" {
		t.Errorf("method=%q content-type=%q", gotMethod, gotType)
	}
	if string(gotBody) != `{"text":"Issue Created: `+goodURL+`"}` {
		t.Errorf("body = %s", gotBody)
	}
}

// The three Slack failure words from spec page 11 must come back as errors
// that quote Slack's own word.
func TestHTTPSenderTurnsSlackErrorsIntoErrors(t *testing.T) {
	cases := []struct {
		status int
		word   string
	}{{400, "invalid_payload"}, {404, "no_service"}, {404, "channel_not_found"}, {500, "server_error"}}
	for _, c := range cases {
		fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			w.Write([]byte(c.word))
		}))
		_, err := NewHTTPSender(fake.URL, nil).Send(context.Background(), []byte(`{}`))
		fake.Close()
		if err == nil || !strings.Contains(err.Error(), c.word) {
			t.Errorf("status %d %q: err = %v", c.status, c.word, err)
		}
	}
}

func TestHTTPSenderTimesOut(t *testing.T) {
	release := make(chan struct{})
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer fake.Close()
	defer close(release)

	client := &http.Client{Timeout: 100 * time.Millisecond}
	start := time.Now()
	_, err := NewHTTPSender(fake.URL, client).Send(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("want a timeout error")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v, the timeout did not apply", time.Since(start))
	}
}

// The webhook URL is the secret. net/http errors normally quote it. Ours must not.
func TestHTTPSenderErrorsNeverContainTheURL(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	secretish := fake.URL + "/services/SECRETMARKER123"
	fake.Close() // nothing listens any more: connection refused

	_, err := NewHTTPSender(secretish, nil).Send(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("want a connection error")
	}
	// The secret part of a Slack URL is its path. (The host:port from the
	// dial error is public information, so it may appear.)
	if strings.Contains(err.Error(), "SECRETMARKER123") || strings.Contains(err.Error(), "/services/") {
		t.Errorf("error leaks the URL: %v", err)
	}
}

// ---- NewHTTPSenderFromEnv (the startup check) ------------------------------------------

func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestStartupErrorWhenSlackURLUnset(t *testing.T) {
	_, err := NewHTTPSenderFromEnv(fakeEnv(nil))
	if err == nil {
		t.Fatal("want an error when SLACK_URL is unset")
	}
	msg := err.Error()
	for _, want := range []string{"SLACK_URL", "not set", "Environment variables"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q should mention %q", msg, want)
		}
	}
}

func TestStartupErrorWhenSlackURLBlank(t *testing.T) {
	if _, err := NewHTTPSenderFromEnv(fakeEnv(map[string]string{"SLACK_URL": "   "})); err == nil {
		t.Error("whitespace-only SLACK_URL should count as unset")
	}
}

func TestStartupAcceptsSpecNameAndFallbackName(t *testing.T) {
	for _, name := range []string{"SLACK_URL", "SLACK_WEBHOOK_URL"} {
		s, err := NewHTTPSenderFromEnv(fakeEnv(map[string]string{name: "https://example.test/abc"}))
		if err != nil || s == nil {
			t.Errorf("%s: err=%v", name, err)
		}
	}
}

func TestStartupRejectsMalformedValueWithoutEchoingIt(t *testing.T) {
	for _, bad := range []string{"not a url", "http://insecure.test/x", "https://", "ht!tp://%zz"} {
		_, err := NewHTTPSenderFromEnv(fakeEnv(map[string]string{"SLACK_URL": bad}))
		if err == nil {
			t.Errorf("%q should be rejected", bad)
			continue
		}
		if strings.Contains(err.Error(), bad) {
			t.Errorf("error echoes the value %q: %v", bad, err)
		}
	}
}

// ---- the Lambda runtime loop against a fake runtime API -------------------------------

type fakeRuntime struct {
	srv       *httptest.Server
	gotPath   string
	gotBody   string
	eventBody string
}

func newFakeRuntime(event string) *fakeRuntime {
	f := &fakeRuntime{eventBody: event}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/invocation/next") {
			w.Header().Set("Lambda-Runtime-Aws-Request-Id", "req-1")
			w.Write([]byte(f.eventBody))
			return
		}
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		f.gotPath, f.gotBody = r.URL.Path, string(b)
		w.WriteHeader(http.StatusAccepted)
	}))
	return f
}

func (f *fakeRuntime) api() string { return strings.TrimPrefix(f.srv.URL, "http://") }

func TestServeOneReportsSuccessAsJSONString(t *testing.T) {
	f := newFakeRuntime(`{"issue":{"html_url":"` + goodURL + `"}}`)
	defer f.srv.Close()
	s := &recordingSender{}
	err := ServeOne(context.Background(), f.api(), func(ctx context.Context, ev []byte) (string, error) {
		return Handle(ctx, ev, s)
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.gotPath != "/2018-06-01/runtime/invocation/req-1/response" || f.gotBody != `"ok"` {
		t.Errorf("posted %s %s", f.gotPath, f.gotBody)
	}
	if len(s.payloads) != 1 {
		t.Errorf("sender called %d times, want 1", len(s.payloads))
	}
}

func TestServeOneReportsHandlerErrors(t *testing.T) {
	f := newFakeRuntime(`{bad json`)
	defer f.srv.Close()
	err := ServeOne(context.Background(), f.api(), func(ctx context.Context, ev []byte) (string, error) {
		return Handle(ctx, ev, &recordingSender{})
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.gotPath != "/2018-06-01/runtime/invocation/req-1/error" || !strings.Contains(f.gotBody, "not valid JSON") {
		t.Errorf("posted %s %s", f.gotPath, f.gotBody)
	}
}

func TestReportInitError(t *testing.T) {
	f := newFakeRuntime("")
	defer f.srv.Close()
	_, startupErr := NewHTTPSenderFromEnv(fakeEnv(nil))
	if err := ReportInitError(context.Background(), f.api(), startupErr); err != nil {
		t.Fatal(err)
	}
	if f.gotPath != "/2018-06-01/runtime/init/error" || !strings.Contains(f.gotBody, "SLACK_URL is not set") {
		t.Errorf("posted %s %s", f.gotPath, f.gotBody)
	}
}
