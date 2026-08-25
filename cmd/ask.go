package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/smeltery/beam/internal/beam"
	"github.com/spf13/cobra"
)

func newAskCmd() *cobra.Command {
	return newAskCommand("ask <prompt>", "Send an interactive prompt")
}

func newNotifyAskCmd() *cobra.Command {
	return newAskCommand("ask <prompt>", "Send an interactive prompt notification")
}

func newAskCommand(use, short string) *cobra.Command {
	var approval, yesNo, text, wait, strict, fromStdin bool
	var callbackURL, callbackToken, correlationID, idempotencyKey string
	var title, imageURL, url string
	var deviceIDs []string
	var timeout, expiresIn, poll time.Duration
	cmd := &cobra.Command{
		Use:   strings.Replace(use, "<prompt>", "[prompt]", 1),
		Short: short,
		Args: func(cmd *cobra.Command, args []string) error {
			if fromStdin {
				if len(args) != 0 {
					return UsageError{Err: fmt.Errorf("prompt argument cannot be used with --stdin")}
				}
				return nil
			}
			if len(args) != 1 {
				return UsageError{Err: fmt.Errorf("accepts 1 arg, received %d", len(args))}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			selected := 0
			kind := ""
			for _, item := range []struct {
				on   bool
				kind string
			}{{approval, "approval"}, {yesNo, "yes_no"}, {text, "text"}} {
				if item.on {
					selected++
					kind = item.kind
				}
			}
			if selected != 1 {
				return UsageError{Err: fmt.Errorf("pass exactly one of --approval, --yes-no, or --text")}
			}
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			body := ""
			if fromStdin {
				data, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				body = strings.TrimRight(string(data), "\r\n")
				if body == "" {
					return UsageError{Err: fmt.Errorf("stdin prompt cannot be empty")}
				}
			} else {
				body = args[0]
			}
			responseExpiry := expiresIn
			if !cmd.Flags().Changed("expires-in") && cmd.Flags().Changed("timeout") {
				responseExpiry = timeout
			}
			payload := beam.NotificationRequest{
				Body:      body,
				Title:     title,
				ImageURL:  imageURL,
				URL:       url,
				DeviceIDs: deviceIDs,
				Response: &beam.ResponseRequest{
					Type:             kind,
					ExpiresInSeconds: int(responseExpiry.Seconds()),
					CorrelationID:    correlationID,
				},
			}
			if callbackURL != "" || callbackToken != "" {
				payload.Response.Callback = &beam.CallbackRequest{
					URL:   callbackURL,
					Token: callbackToken,
				}
			}
			data, err := postJSON(client, hookURL(cfg, ""), payload, idempotencyKey)
			if err != nil {
				return err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			if strict {
				if delivered, ok := out["delivered"].(float64); ok && delivered == 0 {
					if err := json.NewEncoder(cmd.OutOrStdout()).Encode(out); err != nil {
						return err
					}
					return ErrNoDeviceAccepted
				}
			}
			var waitErr error
			if wait {
				eventID, _ := out["eventId"].(string)
				if eventID != "" {
					out, err = waitForInteraction(client, hookURL(cfg, "/events/"+eventID), timeout, poll)
					if err != nil && !errors.Is(err, ErrInteractionTimedOut) {
						return err
					}
					waitErr = err
				}
			}
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(out); err != nil {
				return err
			}
			if waitErr != nil {
				return waitErr
			}
			if wait {
				return interactionOutcomeError(out)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&approval, "approval", false, "ask for approve or deny")
	cmd.Flags().BoolVar(&yesNo, "yes-no", false, "ask for yes or no")
	cmd.Flags().BoolVar(&text, "text", false, "ask for a text reply")
	cmd.Flags().BoolVar(&wait, "wait", false, "poll until the prompt settles or timeout passes")
	cmd.Flags().BoolVar(&fromStdin, "stdin", false, "read prompt from stdin")
	cmd.Flags().BoolVar(&strict, "strict", false, "return exit code 7 when no device accepts the push")
	cmd.Flags().StringVar(&title, "title", "", "sender title")
	cmd.Flags().StringVar(&imageURL, "image", "", "sender image URL")
	cmd.Flags().StringVar(&url, "url", "", "tap destination URL")
	cmd.Flags().StringArrayVar(&deviceIDs, "device", nil, "target device ID, repeatable")
	cmd.Flags().StringVar(&callbackURL, "callback-url", "", "public HTTPS callback URL")
	cmd.Flags().StringVar(&callbackToken, "callback-token", "", "callback bearer token")
	cmd.Flags().StringVar(&correlationID, "correlation-id", "", "caller correlation ID echoed in responses and callbacks")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "deduplicate prompt sends for retry-safe automation")
	cmd.Flags().DurationVar(&expiresIn, "expires-in", 15*time.Minute, "prompt expiry")
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "wait timeout")
	cmd.Flags().DurationVar(&poll, "poll", 2*time.Second, "polling interval when waiting")
	return cmd
}
