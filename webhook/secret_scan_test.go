package webhook

// The spec's biggest penalty: pushing the Slack URL to GitHub costs 2 points
// and revokes the URL for the whole class (page 1, page 4). This test walks
// every file in the repo and fails if anything looks like a Slack webhook URL.
// It runs with `go test ./...`, so a normal test run is also a secret check.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The public, generic shape of a Slack incoming-webhook URL.
var slackURLShape = regexp.MustCompile(`hooks\.slack\.com/(services|workflows)/`)

func TestNoSlackWebhookURLAnywhereInTheRepo(t *testing.T) {
	root := ".." // this test file lives in <repo>/webhook
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "build":
				return filepath.SkipDir
			}
			return nil
		}
		// Skip this scanner and the shell twin of it: both name the pattern.
		if d.Name() == "secret_scan_test.go" || d.Name() == "check-no-secret.sh" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if slackURLShape.Match(data) {
			t.Errorf("%s contains something shaped like a Slack webhook URL; remove it", strings.TrimPrefix(path, root+"/"))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
