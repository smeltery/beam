package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/smeltery/beam/internal/beam"
	"github.com/spf13/cobra"
)

func newNotifyCmd() *cobra.Command {
	var title, imageURL, url, idempotencyKey string
	var fromStdin bool
	var strict bool
	var deviceIDs []string
	cmd := &cobra.Command{
		Use:   "notify [body]",
		Short: "Send a one-shot notification",
		Args: func(cmd *cobra.Command, args []string) error {
			if fromStdin {
				if len(args) != 0 {
					return UsageError{Err: fmt.Errorf("body argument cannot be used with --stdin")}
				}
				return nil
			}
			if len(args) != 1 {
				return UsageError{Err: fmt.Errorf("accepts 1 arg, received %d", len(args))}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
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
					return UsageError{Err: fmt.Errorf("stdin body cannot be empty")}
				}
			} else {
				body = args[0]
			}
			payload := beam.NotificationRequest{
				Body:      body,
				Title:     title,
				ImageURL:  imageURL,
				URL:       url,
				DeviceIDs: deviceIDs,
			}
			data, err := postJSON(client, hookURL(cfg, ""), payload, idempotencyKey)
			if err != nil {
				return err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(out); err != nil {
				return err
			}
			if strict {
				if delivered, ok := out["delivered"].(float64); ok && delivered == 0 {
					return ErrNoDeviceAccepted
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "sender title")
	cmd.Flags().StringVar(&imageURL, "image", "", "sender image URL")
	cmd.Flags().StringVar(&url, "url", "", "tap destination URL")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "safe retry key")
	cmd.Flags().BoolVar(&fromStdin, "stdin", false, "read body from stdin")
	cmd.Flags().BoolVar(&strict, "strict", false, "return exit code 7 when no device accepts the push")
	cmd.Flags().StringArrayVar(&deviceIDs, "device", nil, "target device ID (repeatable)")
	cmd.AddCommand(newNotifyAskCmd())
	return cmd
}
