package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/smeltery/beam/internal/config"
	"github.com/spf13/cobra"
)

var openBrowser = defaultOpenBrowser

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage local CLI credentials",
	}
	cmd.AddCommand(newAuthLoginCmd(), newAuthStatusCmd(), newAuthLogoutCmd(), newAuthConnectionsCmd())
	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	var token, clientName string
	var scopes []string
	var expiresIn, timeout, poll time.Duration
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store local CLI credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			token = firstNonEmpty(token, os.Getenv("BEAM_TOKEN"))
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if token == "" {
				cfg, err = runDeviceLogin(cmd, cfg, clientName, scopes, expiresIn, timeout, poll)
				if err != nil {
					return err
				}
				return writeAuthStatus(cmd, cfg, "config")
			}
			cfg.Token = token
			cfg.Scopes = scopes
			cfg.ClientName = clientName
			cfg.ExpiresAt = ""
			if expiresIn > 0 {
				cfg.ExpiresAt = time.Now().UTC().Add(expiresIn).Format(time.RFC3339)
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			return writeAuthStatus(cmd, cfg, "config")
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "webhook or agent token to store")
	cmd.Flags().StringArrayVar(&scopes, "scope", nil, "credential scope (repeatable)")
	cmd.Flags().StringVar(&clientName, "client-name", "", "human-readable client name")
	cmd.Flags().DurationVar(&expiresIn, "expires-in", 0, "credential lifetime")
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "maximum time to wait for browser authorization")
	cmd.Flags().DurationVar(&poll, "poll", 2*time.Second, "device authorization polling interval")
	return cmd
}

type authDeviceStartResponse struct {
	OK     bool `json:"ok"`
	Device struct {
		DeviceCode string    `json:"deviceCode"`
		UserCode   string    `json:"userCode"`
		VerifyURL  string    `json:"verifyUrl"`
		ExpiresAt  time.Time `json:"expiresAt"`
	} `json:"device"`
}

type authDeviceTokenResponse struct {
	OK         bool      `json:"ok"`
	Status     string    `json:"status"`
	Token      string    `json:"token"`
	Scopes     []string  `json:"scopes"`
	ClientName string    `json:"clientName"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

func runDeviceLogin(cmd *cobra.Command, cfg *config.Config, clientName string, scopes []string, expiresIn, timeout, poll time.Duration) (*config.Config, error) {
	apiCfg, client, err := apiClient()
	if err != nil {
		return cfg, err
	}
	req := map[string]any{
		"clientName": clientName,
		"scopes":     scopes,
	}
	if expiresIn > 0 {
		req["expiresInSeconds"] = int(expiresIn.Seconds())
	}
	data, err := postJSON(client, apiURL(apiCfg, "/api/auth/device"), req, "")
	if err != nil {
		return cfg, err
	}
	var start authDeviceStartResponse
	if err := json.Unmarshal(data, &start); err != nil {
		return cfg, err
	}
	if err := openBrowser(start.Device.VerifyURL); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Could not open browser: %v\n", err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Open %s and enter code %s\n", start.Device.VerifyURL, start.Device.UserCode)
	if poll <= 0 {
		poll = 2 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := getJSON(client, apiURL(apiCfg, "/api/auth/device/"+start.Device.DeviceCode+"/token"))
		if err != nil {
			return cfg, err
		}
		var tokenResp authDeviceTokenResponse
		if err := json.Unmarshal(data, &tokenResp); err != nil {
			return cfg, err
		}
		switch tokenResp.Status {
		case "approved":
			cfg.Token = tokenResp.Token
			cfg.Scopes = tokenResp.Scopes
			cfg.ClientName = tokenResp.ClientName
			cfg.ExpiresAt = ""
			if !tokenResp.ExpiresAt.IsZero() {
				cfg.ExpiresAt = tokenResp.ExpiresAt.Format(time.RFC3339)
			}
			if cfg.ExpiresAt == "" && expiresIn > 0 {
				cfg.ExpiresAt = time.Now().UTC().Add(expiresIn).Format(time.RFC3339)
			}
			return cfg, config.Save(cfg)
		case "expired":
			return cfg, ErrInteractionUnavailable
		}
		time.Sleep(poll)
	}
	return cfg, ErrInteractionTimedOut
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show local auth status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			source := "config"
			if token := os.Getenv("BEAM_TOKEN"); token != "" {
				cfg.Token = token
				source = "env"
			}
			if apiURL := os.Getenv("BEAM_API_URL"); apiURL != "" {
				cfg.APIURL = apiURL
			}
			return writeAuthStatus(cmd, cfg, source)
		},
	}
}

func newAuthLogoutCmd() *cobra.Command {
	var revoke bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove local CLI credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if revoke && cfg.Token != "" {
				if err := revokeAuthToken(cfg); err != nil {
					return err
				}
			}
			cfg.Token = ""
			cfg.Scopes = nil
			cfg.ClientName = ""
			cfg.ExpiresAt = ""
			if err := config.Save(cfg); err != nil {
				return err
			}
			return writeAuthStatus(cmd, cfg, "config")
		},
	}
	cmd.Flags().BoolVar(&revoke, "revoke", false, "revoke the remote credential before clearing local config")
	return cmd
}

func newAuthConnectionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connections",
		Short: "Manage token-safe agent connections",
	}
	cmd.AddCommand(newAuthConnectionsListCmd(), newAuthConnectionsRevokeCmd())
	return cmd
}

func newAuthConnectionsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List token-safe agent connections",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := getJSON(client, apiURL(cfg, "/api/auth/connections"))
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

func newAuthConnectionsRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <connection-id>",
		Short: "Revoke an agent connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := apiClient()
			if err != nil {
				return err
			}
			data, err := postJSON(client, apiURL(cfg, "/api/auth/connections/"+args[0]+"/revoke"), map[string]any{}, "")
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

func revokeAuthToken(cfg *config.Config) error {
	apiCfg, client, err := apiClient()
	if err != nil {
		return err
	}
	_, err = postJSON(client, apiURL(apiCfg, "/api/auth/revoke"), map[string]string{"token": cfg.Token}, "")
	return err
}

func writeAuthStatus(cmd *cobra.Command, cfg *config.Config, source string) error {
	expired := false
	if cfg.ExpiresAt != "" {
		if expiresAt, err := time.Parse(time.RFC3339, cfg.ExpiresAt); err == nil {
			expired = time.Now().UTC().After(expiresAt)
		}
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
		"ok":            true,
		"authenticated": cfg.Token != "" && !expired,
		"apiUrl":        cfg.APIURL,
		"credential": map[string]any{
			"source":     source,
			"configured": cfg.Token != "",
			"clientName": cfg.ClientName,
			"scopes":     cfg.Scopes,
			"expiresAt":  cfg.ExpiresAt,
			"expired":    expired,
		},
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func defaultOpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
