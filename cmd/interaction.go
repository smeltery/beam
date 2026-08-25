package cmd

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/smeltery/beam/internal/beam"
	"github.com/spf13/cobra"
)

func newInteractionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "interaction",
		Short: "Inspect and wait for interactive prompts",
	}

	var timeout, poll time.Duration
	waitCmd := &cobra.Command{
		Use:   "wait <event-id>",
		Short: "Poll an interaction until it settles",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			out, err := waitForInteraction(client, hookURL(cfg, "/events/"+args[0]), timeout, poll)
			if encodeErr := json.NewEncoder(cmd.OutOrStdout()).Encode(out); encodeErr != nil {
				return encodeErr
			}
			if err != nil {
				return err
			}
			return interactionOutcomeError(out)
		},
	}
	waitCmd.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "maximum time to wait")
	waitCmd.Flags().DurationVar(&poll, "poll", 2*time.Second, "polling interval")

	cmd.AddCommand(waitCmd)
	return cmd
}

func waitForInteraction(client *http.Client, url string, timeout, poll time.Duration) (map[string]any, error) {
	if poll <= 0 {
		poll = 2 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var out map[string]any
	for {
		data, err := getJSON(client, url)
		if err != nil {
			return out, err
		}
		out = map[string]any{}
		if err := json.Unmarshal(data, &out); err != nil {
			return out, err
		}
		if isInteractionSettled(out) {
			return out, nil
		}
		if !time.Now().Add(poll).Before(deadline) {
			return out, ErrInteractionTimedOut
		}
		time.Sleep(poll)
	}
}

func isInteractionSettled(out map[string]any) bool {
	event := eventFromOutput(out)
	return event.Response != nil && event.Response.Status != "pending"
}

func interactionOutcomeError(out map[string]any) error {
	event := eventFromOutput(out)
	if event.Response == nil {
		return nil
	}
	switch event.Response.Status {
	case "expired", "canceled":
		return ErrInteractionUnavailable
	case "denied", "no":
		return ErrInteractionDenied
	default:
		return nil
	}
}

func eventFromOutput(out map[string]any) beam.Event {
	raw, _ := json.Marshal(out["event"])
	var event beam.Event
	_ = json.Unmarshal(raw, &event)
	return event
}
