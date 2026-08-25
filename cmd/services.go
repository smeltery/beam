package cmd

import (
	"encoding/json"
	"strings"

	"github.com/smeltery/beam/internal/beam"
	"github.com/spf13/cobra"
)

func newServicesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "services",
		Short: "Manage Beam services",
	}
	cmd.AddCommand(
		newServicesCreateCmd(),
		newServicesListCmd(),
		newServicesShowCmd(),
		newServicesUpdateCmd(),
		newServicesDeleteCmd(),
		newServicesRotateCmd(),
		newServicesEventsCmd(),
		newServicesDevicesCmd(),
	)
	return cmd
}

func newServicesCreateCmd() *cobra.Command {
	var req beam.ServiceCreateRequest
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a service and print its token once",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := postJSON(client, apiURL(cfg, "/api/services"), req, "")
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
	cmd.Flags().StringVar(&req.Title, "title", "", "service title")
	cmd.Flags().StringVar(&req.ImageURL, "image", "", "default sender image URL")
	cmd.Flags().StringVar(&req.URL, "url", "", "default tap URL")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

func newServicesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List services without exposing tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := getJSON(client, apiURL(cfg, "/api/services"))
			if err != nil {
				return err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			scrubServiceSecrets(out)
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		},
	}
}

func newServicesShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show a service without exposing its token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := getJSON(client, apiURL(cfg, "/api/services/"+args[0]))
			if err != nil {
				return err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			scrubServiceSecrets(out)
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		},
	}
}

func newServicesUpdateCmd() *cobra.Command {
	var title, imageURL, url string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update service defaults",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := beam.ServiceUpdateRequest{}
			if cmd.Flags().Changed("title") {
				req.Title = &title
			}
			if cmd.Flags().Changed("image") {
				req.ImageURL = &imageURL
			}
			if cmd.Flags().Changed("url") {
				req.URL = &url
			}
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := patchJSON(client, apiURL(cfg, "/api/services/"+args[0]), req)
			if err != nil {
				return err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			scrubServiceSecrets(out)
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "service title")
	cmd.Flags().StringVar(&imageURL, "image", "", "default sender image URL")
	cmd.Flags().StringVar(&url, "url", "", "default tap URL")
	return cmd
}

func newServicesDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := deleteJSON(client, apiURL(cfg, "/api/services/"+args[0]))
			if err != nil {
				return err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			scrubServiceSecrets(out)
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		},
	}
}

func newServicesRotateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rotate-token <id>",
		Short: "Rotate a service token and print the new token once",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := postJSON(client, apiURL(cfg, "/api/services/"+args[0]+"/rotate-token"), map[string]any{}, "")
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

func newServicesEventsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "events <service-id>",
		Short: "List recent token-safe service events",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := getJSON(client, apiURL(cfg, "/api/services/"+args[0]+"/events"))
			if err != nil {
				return err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			scrubServiceSecrets(out)
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		},
	}
}

func newServicesDevicesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "devices",
		Short: "Manage service devices",
	}
	cmd.AddCommand(newServicesDevicesListCmd(), newServicesDevicesRegisterCmd(), newServicesDevicesDeactivateCmd())
	return cmd
}

func newServicesDevicesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <service-id>",
		Short: "List service devices",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := getJSON(client, apiURL(cfg, "/api/services/"+args[0]+"/devices"))
			if err != nil {
				return err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			scrubServiceSecrets(out)
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		},
	}
}

func newServicesDevicesRegisterCmd() *cobra.Command {
	var req beam.DeviceRegisterRequest
	cmd := &cobra.Command{
		Use:   "register <service-id>",
		Short: "Register an iOS device for a service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := postJSON(client, apiURL(cfg, "/api/services/"+args[0]+"/devices"), req, "")
			if err != nil {
				return err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			scrubServiceSecrets(out)
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		},
	}
	cmd.Flags().StringVar(&req.Name, "name", "", "device display name")
	cmd.Flags().StringVar(&req.Platform, "platform", "ios", "device platform")
	cmd.Flags().StringVar(&req.PushToken, "push-token", "", "notification push token")
	cmd.Flags().StringVar(&req.PushToStartToken, "push-to-start-token", "", "Live Activity push-to-start token")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func scrubServiceSecrets(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if isServiceSecretField(key) {
				delete(v, key)
				continue
			}
			scrubServiceSecrets(child)
		}
	case []any:
		for _, child := range v {
			scrubServiceSecrets(child)
		}
	}
}

func isServiceSecretField(key string) bool {
	switch strings.ToLower(key) {
	case "token", "pushtoken", "pushtostarttoken", "callbacktoken", "providertoken":
		return true
	default:
		return false
	}
}

func newServicesDevicesDeactivateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deactivate <service-id> <device-id>",
		Short: "Mark a service device inactive",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := postJSON(client, apiURL(cfg, "/api/services/"+args[0]+"/devices/"+args[1]+"/deactivate"), map[string]any{}, "")
			if err != nil {
				return err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			scrubServiceSecrets(out)
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		},
	}
}
