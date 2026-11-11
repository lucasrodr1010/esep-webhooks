// Command lambda is the program that runs inside AWS Lambda.
// Build it as "bootstrap" (see scripts/build.sh).
package main

import (
	"context"
	"log"
	"os"

	"esep/webhooks/webhook"
)

func main() {
	ctx := context.Background()

	// Lambda sets this to the address of its local "runtime API".
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	if api == "" {
		log.Fatal("AWS_LAMBDA_RUNTIME_API is not set: this program only runs inside AWS Lambda. " +
			"For a local run use: go run ./cmd/local")
	}

	// Startup check: the Slack URL must be in the Lambda environment variable
	// SLACK_URL. If it is not, fail loudly now, with a message that names the
	// fix, instead of failing later on every GitHub delivery.
	sender, err := webhook.NewHTTPSenderFromEnv(os.Getenv)
	if err != nil {
		log.Printf("startup error: %v", err)
		webhook.ReportInitError(ctx, api, err)
		os.Exit(1)
	}

	handler := func(ctx context.Context, event []byte) (string, error) {
		// The spec's starter logs "Received: <input>" (page 10). This shows up
		// in CloudWatch and helps debugging. It is GitHub's payload, which
		// holds no secret; the Slack URL is never logged.
		log.Printf("Received: %s", event)

		result, err := webhook.Handle(ctx, event, sender)
		if err != nil {
			log.Printf("error: %v", err)
		} else {
			log.Printf("result: %s", result)
		}
		return result, err
	}

	if err := webhook.Serve(ctx, api, handler); err != nil {
		log.Fatalf("runtime loop stopped: %v", err)
	}
}
