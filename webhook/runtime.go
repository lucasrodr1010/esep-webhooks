package webhook

// This file is a tiny AWS Lambda "custom runtime" client, using only Go's
// standard library.
//
// Why it exists: Go has no built-in Lambda runtime. Lambda runs a Go program
// on the "provided.al2023" runtime: your zip holds an executable named
// "bootstrap", and Lambda sets AWS_LAMBDA_RUNTIME_API to a local address.
// The program then loops:
//
//	GET  http://$AWS_LAMBDA_RUNTIME_API/2018-06-01/runtime/invocation/next
//	     -> the response BODY is the event; header Lambda-Runtime-Aws-Request-Id
//	POST .../runtime/invocation/<id>/response   (the result, as JSON)
//	POST .../runtime/invocation/<id>/error      (if the handler failed)
//	POST .../runtime/init/error                 (if startup failed)
//
// The AWS-supplied library aws-lambda-go does exactly this; it is written out
// by hand here so the project has zero third-party dependencies (like A5).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// HandlerFunc turns one event into one string result.
type HandlerFunc func(ctx context.Context, event []byte) (string, error)

// runtimeClient has NO timeout on purpose: "next" is a long poll that blocks
// until Lambda has an event, possibly for a long while between invocations.
var runtimeClient = &http.Client{}

// ServeOne handles exactly one invocation (used in a loop by Serve, and
// directly by tests). api is the host:port from AWS_LAMBDA_RUNTIME_API.
func ServeOne(ctx context.Context, api string, handler HandlerFunc) error {
	base := "http://" + api + "/2018-06-01/runtime/"

	// 1. Ask for the next event.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"invocation/next", nil)
	if err != nil {
		return err
	}
	resp, err := runtimeClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching next invocation: %w", err)
	}
	event, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return fmt.Errorf("reading event: %w", err)
	}
	requestID := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
	if requestID == "" {
		return fmt.Errorf("runtime API sent no request id (HTTP %d)", resp.StatusCode)
	}

	// 2. Run our code.
	result, handlerErr := handler(ctx, event)

	// 3. Report the outcome.
	if handlerErr != nil {
		return post(ctx, base+"invocation/"+requestID+"/error", errorBody(handlerErr))
	}
	// The result must be JSON. A Go string becomes a JSON string, e.g. "ok",
	// which API Gateway hands back to GitHub / Postman.
	out, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return post(ctx, base+"invocation/"+requestID+"/response", out)
}

// Serve loops forever (until ctx is cancelled or the runtime API fails).
func Serve(ctx context.Context, api string, handler HandlerFunc) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := ServeOne(ctx, api, handler); err != nil {
			return err
		}
	}
}

// ReportInitError tells Lambda that startup failed (for example the Slack URL
// is missing). Lambda then shows the message in the test console and in
// CloudWatch instead of a mysterious timeout.
func ReportInitError(ctx context.Context, api string, cause error) error {
	return post(ctx, "http://"+api+"/2018-06-01/runtime/init/error", errorBody(cause))
}

func errorBody(err error) []byte {
	b, _ := json.Marshal(map[string]string{
		"errorMessage": err.Error(),
		"errorType":    "WebhookError",
	})
	return b
}

func post(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := runtimeClient.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("runtime API answered HTTP %d for %s", resp.StatusCode, url)
	}
	return nil
}
