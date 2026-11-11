package main

import (
	"bytes"
	"strings"
	"testing"
)

func noEnv(string) string { return "" }

func runLocal(t *testing.T, args []string, stdin string, env func(string) string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errb, env)
	return code, out.String(), errb.String()
}

const ev = `{"issue":{"html_url":"https://github.com/a/b/issues/1"}}`

func TestDefaultModeIsDryRun(t *testing.T) {
	code, out, _ := runLocal(t, []string{"-event", "-"}, ev, noEnv)
	if code != 0 || !strings.Contains(out, "[dry-run] would POST") || !strings.Contains(out, `"ok"`) {
		t.Errorf("code=%d out=%q", code, out)
	}
}

func TestMockModeUsesTheRealHTTPPathOnLocalhost(t *testing.T) {
	code, out, _ := runLocal(t, []string{"-mock", "-event", "-"}, ev, noEnv)
	if code != 0 || !strings.Contains(out, "[mock-slack] got POST") || !strings.Contains(out, "Issue Created: https://github.com/a/b/issues/1") {
		t.Errorf("code=%d out=%q", code, out)
	}
}

func TestLiveModeWithoutSlackURLFailsClearlyAndSendsNothing(t *testing.T) {
	code, _, errOut := runLocal(t, []string{"-live", "-event", "-"}, ev, noEnv)
	if code == 0 || !strings.Contains(errOut, "SLACK_URL is not set") {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
}

func TestTwoModesAtOnceIsRefused(t *testing.T) {
	if code, _, _ := runLocal(t, []string{"-mock", "-live", "-event", "-"}, ev, noEnv); code != 2 {
		t.Errorf("code=%d, want 2", code)
	}
}

func TestPingEventPostsNothing(t *testing.T) {
	code, out, _ := runLocal(t, []string{"-event", "-"}, `{"zen":"hi","hook_id":1}`, noEnv)
	if code != 0 || strings.Contains(out, "[dry-run]") || !strings.Contains(out, "ignored") {
		t.Errorf("code=%d out=%q", code, out)
	}
}
