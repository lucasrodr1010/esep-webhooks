#!/usr/bin/env bash
# Pre-push safety check for the spec's -2 point rule (page 1 and page 4):
# the Slack URL must appear nowhere in the repo. Run before every commit/push:
#   bash scripts/check-no-secret.sh
set -uo pipefail
cd "$(dirname "$0")/.."
# Slack incoming-webhook URLs have this public, generic shape.
PATTERN='hooks\.slack\.com/(services|workflows)/'
bad=0
if grep -rEn --exclude-dir=.git --exclude-dir=build --exclude=check-no-secret.sh \
     --exclude=secret_scan_test.go "$PATTERN" . ; then bad=1; fi
if git rev-parse --git-dir >/dev/null 2>&1; then
  if git diff --cached | grep -E "$PATTERN" >/dev/null; then echo "staged diff contains a Slack URL"; bad=1; fi
fi
if [ "$bad" = 1 ]; then echo "FAIL: Slack URL found. Remove it before committing."; exit 1; fi
echo "OK: no Slack webhook URL found in the repo"
