package call

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tello-ai/tello-go/tello"

	"github.com/tello-ai/tello-cli/internal/app"
)

// summaryFields are copied from the call.summary frame as sent, so gateway
// nulls stay null.
var summaryFields = []string{"callId", "status", "durationSeconds", "transcript", "summary", "creditCharged"}

func newSummary(a *app.App) *cobra.Command {
	timeout := 15 * time.Second
	cmd := &cobra.Command{
		Use:   "summary <callId>",
		Short: "Show the summary and transcript of a completed call",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 || args[0] == "" {
				return app.Usagef("expected exactly one <callId>")
			}
			if timeout <= 0 {
				return app.Usagef("--timeout must be positive")
			}
			data, err := fetchSummary(cmd.Context(), a, args[0], timeout)
			if err != nil {
				return err
			}
			if !a.JSON {
				printSummary(a, data)
			}
			return a.WriteResult(data)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", timeout, "how long to wait for the summary")
	return cmd
}

func fetchSummary(ctx context.Context, a *app.App, callID string, timeout time.Duration) (map[string]any, error) {
	client, _, err := a.NewClient()
	if err != nil {
		return nil, err
	}
	defer client.Close()
	requestID, err := newRequestID()
	if err != nil {
		return nil, err
	}
	// SDK handlers run on the receive goroutine: hand over without blocking.
	reply := make(chan tello.Event, 1)
	deliver := func(_ context.Context, ev tello.Event) error {
		if ev.Type == tello.EventTypeDisconnected || ev.RequestID == requestID {
			select {
			case reply <- ev:
			default:
			}
		}
		return nil
	}
	client.On(tello.EventTypeCallSummary, deliver)
	client.On(tello.EventTypeError, deliver)
	client.On(tello.EventTypeDisconnected, deliver)

	if err := a.Connect(ctx, client); err != nil {
		return nil, err
	}
	if err := client.GetSummary(context.Background(), callID, requestID); err != nil {
		return nil, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case ev := <-reply:
		switch ev.Type {
		case tello.EventTypeCallSummary:
			data := make(map[string]any, len(summaryFields))
			for _, k := range summaryFields {
				data[k] = ev.Raw[k]
			}
			return data, nil
		case tello.EventTypeError:
			return nil, tello.ErrorFor(ev.Code, ev.Message, ev.Question)
		default:
			return nil, app.NewError(app.KindConnection, "connectionClosed", "connection closed before the summary arrived")
		}
	case <-timer.C:
		// Not `timeout`, which means a call cancelled by --timeout (exit 6).
		return nil, app.NewError(app.KindConnection, "summaryTimeout", fmt.Sprintf("no summary within %s", timeout))
	case <-ctx.Done():
		return nil, app.NewError(app.KindInterrupted, "interrupted", "interrupted")
	}
}

func printSummary(a *app.App, data map[string]any) {
	a.Printf("callId:          %s\n", show(data["callId"]))
	a.Printf("status:          %s\n", show(data["status"]))
	a.Printf("durationSeconds: %s\n", show(data["durationSeconds"]))
	a.Printf("creditCharged:   %s\n", show(data["creditCharged"]))
	a.Printf("\nsummary:\n%s\n", indent(show(data["summary"])))
	a.Printf("\ntranscript:\n%s\n", indent(show(data["transcript"])))
}

func show(v any) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(v)
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}

func newRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate requestId: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
