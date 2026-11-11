// Command local runs the same handler as the Lambda, but on your own machine,
// reading the GitHub event from a file instead of from API Gateway.
//
// Three modes (pick at most one; the default is -dry-run):
//
//	-dry-run  print the Slack message that WOULD be sent; sends nothing
//	-mock     start a fake Slack on 127.0.0.1 inside this process and POST to
//	          it with the real HTTP code; still sends nothing to the internet
//	-live     POST to the real Slack webhook in $SLACK_URL. Only you run this.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	"esep/webhooks/webhook"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

// run is separate from main so it can be tested without exiting the process.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("local", flag.ContinueOnError)
	fs.SetOutput(stderr)
	eventPath := fs.String("event", "events/issue-opened.json", `GitHub event JSON file ("-" reads stdin)`)
	dry := fs.Bool("dry-run", false, "print the Slack message instead of sending it (default mode)")
	mock := fs.Bool("mock", false, "post to a fake Slack server on 127.0.0.1")
	live := fs.Bool("live", false, "post to the real Slack webhook in $SLACK_URL")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	chosen := 0
	for _, b := range []bool{*dry, *mock, *live} {
		if b {
			chosen++
		}
	}
	if chosen > 1 {
		fmt.Fprintln(stderr, "choose only one of -dry-run, -mock, -live")
		return 2
	}

	// Read the event.
	var event []byte
	var err error
	if *eventPath == "-" {
		event, err = io.ReadAll(stdin)
	} else {
		event, err = os.ReadFile(*eventPath)
	}
	if err != nil {
		fmt.Fprintf(stderr, "cannot read event: %v\n", err)
		return 1
	}

	// Pick the sender for the chosen mode.
	var sender webhook.Sender
	switch {
	case *live:
		s, err := webhook.NewHTTPSenderFromEnv(getenv) // clear error if unset
		if err != nil {
			fmt.Fprintf(stderr, "startup error: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "[live] posting to the real Slack webhook")
		sender = s
	case *mock:
		fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			fmt.Fprintf(stdout, "[mock-slack] got %s with Content-Type %q and body %s\n",
				r.Method, r.Header.Get("Content-Type"), body)
			fmt.Fprint(w, "ok") // what real Slack answers on success
		}))
		defer fake.Close()
		fmt.Fprintf(stdout, "[mock] fake Slack listening on %s (local only)\n", fake.URL)
		sender = webhook.NewHTTPSender(fake.URL, nil)
	default:
		sender = webhook.DryRunSender{Out: stdout}
	}

	result, err := webhook.Handle(context.Background(), event, sender)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "function returned: %q\n", result)
	return 0
}
