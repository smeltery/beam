package cmd

import (
	"encoding/json"
	"time"

	"github.com/smeltery/beam/internal/beam"
	"github.com/spf13/cobra"
)

func newActivityCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "activity", Short: "Drive Live Activity state"}
	cmd.AddCommand(newActivityStartCmd(), newActivityUpdateCmd(), newActivityEndCmd(), newActivityGetCmd(), newActivityListCmd())
	return cmd
}

func newActivityStartCmd() *cobra.Command {
	var req beam.ActivityRequest
	var idempotencyKey string
	var strict bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a Live Activity",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := postJSON(client, hookURL(cfg, "/live-activities"), req, idempotencyKey)
			if err != nil {
				return err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			return writeActivityOutput(cmd, out, strict)
		},
	}
	activityFlags(cmd, &req, &strict, false, false)
	cmd.Flags().StringVar(&req.Key, "key", "", "stable activity key")
	cmd.Flags().StringArrayVar(&req.DeviceIDs, "device", nil, "target device ID, repeatable")
	cmd.Flags().BoolVar(&req.Replace, "replace", false, "replace an existing activity for the key or device")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "safe retry key")
	return cmd
}

func newActivityUpdateCmd() *cobra.Command {
	var req beam.ActivityRequest
	var strict bool
	cmd := &cobra.Command{
		Use:   "update <id-or-key>",
		Short: "Update a Live Activity",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := patchJSON(client, hookURL(cfg, "/live-activities/"+args[0]), req)
			if err != nil {
				return err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			return writeActivityOutput(cmd, out, strict)
		},
	}
	activityFlags(cmd, &req, &strict, false, true)
	return cmd
}

func newActivityEndCmd() *cobra.Command {
	var req beam.ActivityRequest
	var strict bool
	cmd := &cobra.Command{
		Use:   "end <id-or-key>",
		Short: "End a Live Activity",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := postJSON(client, hookURL(cfg, "/live-activities/"+args[0]+"/end"), req, "")
			if err != nil {
				return err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			return writeActivityOutput(cmd, out, strict)
		},
	}
	activityFlags(cmd, &req, &strict, true, true)
	return cmd
}

func newActivityGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id-or-key>",
		Short: "Read a Live Activity",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := getJSON(client, hookURL(cfg, "/live-activities/"+args[0]))
			if err != nil {
				return err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		},
	}
}

func newActivityListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Live Activities",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := getJSON(client, hookURL(cfg, "/live-activities"))
			if err != nil {
				return err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		},
	}
}

func activityFlags(cmd *cobra.Command, req *beam.ActivityRequest, strict *bool, endFields, clearFields bool) {
	cmd.Flags().StringVar(&req.Title, "title", "", "activity title")
	cmd.Flags().StringVar(&req.Status, "status", "", "activity status")
	cmd.Flags().StringVar(&req.Symbol, "symbol", "", "symbol name")
	cmd.Flags().StringVar(&req.AccentColor, "accent-color", "", "accent color as #RRGGBB")
	cmd.Flags().StringVar(&req.Style, "style", "", "layout style")
	cmd.Flags().StringVar(&req.PrivacyMode, "privacy-mode", "", "privacy mode")
	cmd.Flags().Float64("progress", -1, "progress from 0 to 1")
	cmd.Flags().String("detail", "", "activity detail")
	if clearFields {
		cmd.Flags().Bool("clear-detail", false, "clear activity detail")
		cmd.Flags().Bool("clear-progress", false, "clear activity progress")
	}
	cmd.Flags().Int("if-sequence", 0, "expected current sequence")
	cmd.Flags().Duration("expires-in", 0, "activity expiry duration")
	cmd.Flags().Duration("stale-after", 0, "duration before the activity is stale")
	if endFields {
		cmd.Flags().Duration("dismiss-after", 0, "duration before the ended activity is dismissed")
	}
	cmd.Flags().BoolVar(strict, "strict", false, "return exit code 7 when no device accepts the push")
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		progress, err := cmd.Flags().GetFloat64("progress")
		if err != nil {
			return err
		}
		if progress >= 0 {
			req.Progress = &progress
			req.ProgressSet = true
		}
		detail, err := cmd.Flags().GetString("detail")
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("detail") {
			req.Detail = &detail
			req.DetailSet = true
		}
		if clearFields {
			clearDetail, err := cmd.Flags().GetBool("clear-detail")
			if err != nil {
				return err
			}
			if clearDetail {
				req.Detail = nil
				req.DetailSet = true
			}
			clearProgress, err := cmd.Flags().GetBool("clear-progress")
			if err != nil {
				return err
			}
			if clearProgress {
				req.Progress = nil
				req.ProgressSet = true
			}
		}
		ifSequence, err := cmd.Flags().GetInt("if-sequence")
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("if-sequence") {
			req.IfSequence = &ifSequence
		}
		expiresIn, err := cmd.Flags().GetDuration("expires-in")
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("expires-in") {
			req.ExpiresInSeconds = secondsPtr(expiresIn)
		}
		staleAfter, err := cmd.Flags().GetDuration("stale-after")
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("stale-after") {
			req.StaleAfterSeconds = secondsPtr(staleAfter)
		}
		if endFields {
			dismissAfter, err := cmd.Flags().GetDuration("dismiss-after")
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("dismiss-after") {
				req.DismissAfterSeconds = secondsPtr(dismissAfter)
			}
		}
		return nil
	}
}

func secondsPtr(duration time.Duration) *int {
	seconds := int(duration.Seconds())
	return &seconds
}

func writeActivityOutput(cmd *cobra.Command, out map[string]any, strict bool) error {
	if err := json.NewEncoder(cmd.OutOrStdout()).Encode(out); err != nil {
		return err
	}
	if strict {
		if accepted, ok := out["accepted"].(float64); ok && accepted == 0 {
			return ErrNoDeviceAccepted
		}
	}
	return nil
}
