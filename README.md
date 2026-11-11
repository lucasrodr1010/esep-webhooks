# esep-webhooks

ESEP Assignment 8 (Event-Driven Architecture). When someone opens an issue on this repo, GitHub sends a webhook to an AWS API Gateway URL, which runs an AWS Lambda function, which posts `Issue Created: <issue link>` to `#eventing-webhook` in the section's Slack workspace.

```
GitHub issue opened
   -> GitHub webhook (HTTP POST, JSON)
   -> API Gateway (REST API, stage "default")
   -> Lambda function "EsepWebhook" (this repo)
   -> Slack incoming webhook  ->  #eventing-webhook
```

The Lambda is written in Go (the spec's template is C#, but it allows another language: page 2, "Using another language?"). It uses only Go's standard library, no third-party packages, like the A5 grade calculator.

## The Slack URL is a secret

The Slack webhook URL goes in **one place only**: the Lambda environment variable `SLACK_URL` (spec page 4). It is never in code, a README, a screenshot, `aws-lambda-tools-defaults.json`, or a commit. If it reaches GitHub it is revoked for the whole class and costs 2 points.

- The program reads `SLACK_URL` at startup. If it is missing, Lambda reports `SLACK_URL is not set: ...` (visible in the Test tab and CloudWatch) instead of failing quietly on every delivery.
- The code never logs the URL and strips it from network error messages.
- `go test ./...` fails if any file in the repo looks like a Slack webhook URL. `bash scripts/check-no-secret.sh` does the same check, and also looks at your staged diff. Run it before every push.
- For a local run against real Slack you export the variable yourself in your own terminal: `export SLACK_URL='...'`. Do not paste it into a file.

## Project layout

| Path | What it is |
|------|------------|
| `go.mod` | Go module `esep/webhooks`. No dependencies. Asks for Go 1.24.4 (the same as A5). |
| `webhook/webhook.go` | The logic: `ExtractIssueURL` (read GitHub's JSON), `Payload` (build Slack's JSON), `HTTPSender` (real Slack) and `DryRunSender` (prints instead), `Handle` (the whole function). |
| `webhook/runtime.go` | A small Lambda runtime client, so Go can run on Lambda's `provided.al2023` runtime without the `aws-lambda-go` package. |
| `cmd/lambda/main.go` | The program that runs inside Lambda (built as `bootstrap`). Checks `SLACK_URL`, then serves events. |
| `cmd/local/main.go` | Runs the same handler on your Mac from a JSON file: `-dry-run`, `-mock` or `-live`. |
| `events/` | Sample GitHub events: an opened issue, the spec's minimal Postman body, a `ping`, and the bare string `"test"`. They use the placeholder user `your-github-username`. |
| `scripts/build.sh` | Builds `build/function.zip` for Lambda (Linux arm64). |
| `scripts/deploy.sh` | Creates or updates the `EsepWebhook` function in `us-east-2` with the AWS CLI. Never touches `SLACK_URL`. |
| `scripts/check-no-secret.sh` | Fails if a Slack URL is anywhere in the repo or the staged diff. |
| `webhook/*_test.go`, `cmd/local/main_test.go` | Unit tests (standard `testing` package, fake Slack on 127.0.0.1). |
| `alt-csharp/Function.cs` | The same function in C# for the spec's default toolchain. Not compiled or tested, not used. |

## What the function does

1. Takes the event: the JSON GitHub sent (API Gateway's Lambda proxy integration is OFF, so the event is GitHub's body as is).
2. Reads `issue.html_url`.
3. POSTs `{"text":"Issue Created: <link>"}` to the Slack URL, with an 8 second timeout (GitHub waits 10).
4. Returns Slack's answer, `"ok"`.

Edge cases, on purpose:

| Event | Result |
|-------|--------|
| Issue opened, edited, closed, labelled (any `issues` event) | Posts `Issue Created: <link>`. The spec says editing and closing also post, "that's expected". |
| GitHub's `ping` (sent when the webhook is created), or Postman's `"test"` | Nothing posted, answers 200 with `ignored: not an issue event`. |
| Not valid JSON | Error, nothing posted. |
| `issue.html_url` that is not `https://github.com/...` | Error, nothing posted. The API URL is public and the GitHub secret is empty (spec Part 4), so this stops anyone relaying arbitrary links to the channel. |
| Slack answers `invalid_payload`, `no_service`, `channel_not_found`, or any non-2xx | Error that quotes Slack's word and says the `SLACK_URL` value is wrong or revoked (spec page 11). |

## Requirements

- Go 1.24.4 or newer. With an older Go, 1.21 or newer, the first `go` command downloads the 1.24.4 toolchain by itself. (Go 1.22's own test binaries crash on this Mac's macOS with `missing LC_UUID`, which is why `go.mod` asks for 1.24.4.)
- For deploying: `zip`, and AWS CLI v2 configured with `aws configure` (region `us-east-2`, spec pages 2-3).

## Run the tests

From the repo root:

```
go vet ./...
go test ./...
go test -race -cover -v ./...
bash scripts/check-no-secret.sh
```

## Run it locally without sending anything to Slack

```
go run ./cmd/local                                          # dry-run is the default: prints what would be sent
go run ./cmd/local -dry-run -event events/postman-minimal.json
go run ./cmd/local -event events/ping.json                   # posts nothing
go run ./cmd/local -mock -event events/issue-opened.json    # real HTTP code, fake Slack on 127.0.0.1
cat events/issue-opened.json | go run ./cmd/local -event -   # event from stdin
```

`-live` posts to the REAL Slack channel. Only run it yourself, after exporting the variable in your own terminal (never put the value in a file):

```
export SLACK_URL='<the webhook URL from Canvas>'
go run ./cmd/local -live -event events/postman-minimal.json
```

Without `SLACK_URL` set, `-live` stops with a clear `SLACK_URL is not set` message and sends nothing.

Example output of the first command:

```
[dry-run] would POST to Slack: {"text":"Issue Created: https://github.com/your-github-username/esep-webhooks/issues/1"}
function returned: "ok"
```

## Deploy (spec Part 1 and Part 5)

Do the Prerequisites on spec page 11 and the AWS credentials steps on pages 2-3 first.

1. Create the role once, in the console: IAM -> Roles -> Create role -> AWS service -> Lambda -> attach `AWSLambdaBasicExecutionRole` -> name it `EsepWebhook-role`. Copy its ARN.
2. First deploy:

```
ROLE_ARN=arn:aws:iam::<your-account-id>:role/EsepWebhook-role bash scripts/deploy.sh
```

3. Every later deploy (Part 5, step 2, "Redeploy"):

```
bash scripts/deploy.sh
```

4. In the Lambda console (region US East (Ohio) us-east-2): `EsepWebhook` -> Configuration -> Environment variables -> Edit -> add key `SLACK_URL` with the webhook URL from Canvas -> Save. Check it is still there after every deploy.
5. Test tab: Create new event `test-string`, Event JSON `"test"`, Test. Expected: `"ignored: not an issue event"` (the starter's `"TEST THIS"` no longer applies). Then test with the body from spec page 10, using your own username. That one posts to Slack:

```
{ "issue": { "html_url": "https://github.com/<your-username>/esep-webhooks/issues/1" } }
```

## API Gateway and the GitHub webhook (spec Parts 3 and 4)

Console steps, done by hand exactly as the spec describes:

- Part 3 (pages 5-7): REST API `Esep-Webhook-Api`, Regional; POST method on `/`; integration Lambda function `EsepWebhook`; **Lambda proxy integration OFF**; Deploy API to a new stage `default`; copy the Invoke URL (ends in `/default`, nothing after it).
- Part 4 (pages 8-9): repo Settings -> Webhooks -> Add webhook. Payload URL = the Invoke URL. Content type `application/json`. Secret empty. SSL verification on. "Let me select individual events": tick **Issues** only. Active. Add webhook.

Then open an issue on this repo. Within a few seconds `Issue Created: <link>` appears in `#eventing-webhook`. If not: GitHub webhook -> Recent Deliveries; Lambda -> Monitor -> View CloudWatch logs; a Slack answer of `invalid_payload`, `no_service` or `channel_not_found` means `SLACK_URL` is wrong or revoked.
