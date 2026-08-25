package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/smeltery/beam/internal/beam"
	"github.com/spf13/cobra"
)

type workerDeliveryRequest struct {
	Operation    string            `json:"operation"`
	EventID      string            `json:"eventId,omitempty"`
	Notification map[string]string `json:"notification,omitempty"`
	Activity     *struct {
		State    beam.ActivityState `json:"state"`
		Sequence int                `json:"sequence"`
	} `json:"activity,omitempty"`
	DeviceIDs []string             `json:"deviceIds,omitempty"`
	Devices   []workerTargetDevice `json:"devices,omitempty"`
	CreatedAt time.Time            `json:"createdAt"`
}

type workerTargetDevice struct {
	DeviceID         string `json:"deviceId"`
	PushToken        string `json:"pushToken,omitempty"`
	PushToStartToken string `json:"pushToStartToken,omitempty"`
}

func newProviderWorkerCmd() *cobra.Command {
	var addr, token, providerName, mode, apnsTopic, apnsEnvironment, apnsKeyID, apnsTeamID, apnsPrivateKeyPath, apnsPrivateKey string
	cmd := &cobra.Command{
		Use:   "provider-worker",
		Short: "Run a token-safe HTTP push provider worker",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := providerWorkerConfig{
				Mode: mode, ProviderName: providerName, Token: token, APNSTopic: apnsTopic, APNSEnvironment: apnsEnvironment,
				APNSKeyID: apnsKeyID, APNSTeamID: apnsTeamID, APNSPrivateKeyPath: apnsPrivateKeyPath, APNSPrivateKeyPEM: apnsPrivateKey,
			}
			if cfg.APNSPrivateKeyPEM == "" && cfg.APNSPrivateKeyPath != "" {
				key, err := os.ReadFile(cfg.APNSPrivateKeyPath)
				if err != nil {
					return err
				}
				cfg.APNSPrivateKeyPEM = string(key)
			}
			if err := cfg.validate(); err != nil {
				return err
			}
			server := &http.Server{Addr: addr, Handler: providerWorkerHandler(cfg)}
			if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "beam provider worker listening on %s\n", addr); err != nil {
				return err
			}
			return server.ListenAndServe()
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8081", "listen address")
	cmd.Flags().StringVar(&token, "token", "", "required bearer token")
	cmd.Flags().StringVar(&providerName, "provider-name", "http-worker", "provider name for diagnostics")
	cmd.Flags().StringVar(&mode, "mode", "diagnostic", "worker mode: diagnostic, apns, or expo")
	cmd.Flags().StringVar(&apnsTopic, "apns-topic", "", "APNs bundle topic")
	cmd.Flags().StringVar(&apnsEnvironment, "apns-environment", "sandbox", "APNs environment: sandbox or production")
	cmd.Flags().StringVar(&apnsKeyID, "apns-key-id", "", "APNs signing key ID")
	cmd.Flags().StringVar(&apnsTeamID, "apns-team-id", "", "Apple developer team ID")
	cmd.Flags().StringVar(&apnsPrivateKeyPath, "apns-private-key-path", "", "path to APNs .p8 private key")
	cmd.Flags().StringVar(&apnsPrivateKey, "apns-private-key", "", "APNs .p8 private key PEM")
	return cmd
}

type providerWorkerConfig struct {
	Mode               string
	ProviderName       string
	Token              string
	ExpoEndpoint       string
	ExpoClient         *http.Client
	APNSTopic          string
	APNSEnvironment    string
	APNSKeyID          string
	APNSTeamID         string
	APNSPrivateKeyPath string
	APNSPrivateKeyPEM  string
	APNSBaseURL        string
	APNSClient         *http.Client
}

func (cfg providerWorkerConfig) validate() error {
	switch workerMode(cfg.Mode) {
	case "diagnostic":
		return nil
	case "expo":
		return nil
	case "apns":
		if strings.TrimSpace(cfg.APNSTopic) == "" {
			return fmt.Errorf("--apns-topic is required when --mode=apns")
		}
		if strings.TrimSpace(cfg.APNSKeyID) == "" || strings.TrimSpace(cfg.APNSTeamID) == "" || strings.TrimSpace(cfg.APNSPrivateKeyPEM) == "" {
			return fmt.Errorf("--apns-key-id, --apns-team-id, and --apns-private-key are required when --mode=apns")
		}
		switch strings.TrimSpace(strings.ToLower(cfg.APNSEnvironment)) {
		case "sandbox", "production":
			return nil
		default:
			return fmt.Errorf("--apns-environment must be sandbox or production")
		}
	default:
		return fmt.Errorf("unknown provider worker mode %q", cfg.Mode)
	}
}

func providerWorkerHandler(cfg providerWorkerConfig) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWorkerJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method_not_allowed"})
			return
		}
		writeWorkerJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("/deliver", func(w http.ResponseWriter, r *http.Request) {
		handleProviderDelivery(w, r, cfg)
	})
	return mux
}

func handleProviderDelivery(w http.ResponseWriter, r *http.Request, cfg providerWorkerConfig) {
	if r.Method != http.MethodPost {
		writeWorkerJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method_not_allowed"})
		return
	}
	if cfg.Token != "" && r.Header.Get("Authorization") != "Bearer "+cfg.Token {
		writeWorkerJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
		return
	}
	var req workerDeliveryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeWorkerJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_json"})
		return
	}
	if workerMode(cfg.Mode) == "apns" {
		now := time.Now().UTC()
		bearer, err := apnsBearerToken(cfg, now)
		if err != nil {
			writeWorkerJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": "apns_auth_failed"})
			return
		}
		apnsReqs, err := apnsRequests(cfg, req, bearer)
		if err != nil {
			writeWorkerJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": "apns_request_failed"})
			return
		}
		diagnostics, err := sendAPNSRequests(cfg, req.Operation, apnsReqs, now)
		if err != nil {
			writeWorkerJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": "apns_delivery_failed"})
			return
		}
		writeWorkerJSON(w, http.StatusOK, map[string]any{"diagnostics": diagnostics})
		return
	}
	if workerMode(cfg.Mode) == "expo" {
		diagnostics, err := sendExpoRequests(cfg, req, time.Now().UTC())
		if err != nil {
			writeWorkerJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": "expo_delivery_failed"})
			return
		}
		writeWorkerJSON(w, http.StatusOK, map[string]any{"diagnostics": diagnostics})
		return
	}
	diagnostics := workerDiagnostics(cfg.ProviderName, req)
	writeWorkerJSON(w, http.StatusOK, map[string]any{"diagnostics": diagnostics})
}

func workerMode(mode string) string {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		return "diagnostic"
	}
	return mode
}

func workerDiagnostics(providerName string, req workerDeliveryRequest) []beam.ProviderDiagnostic {
	now := req.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if providerName == "" {
		providerName = "http-worker"
	}
	targets := req.Devices
	if len(targets) == 0 {
		for _, id := range req.DeviceIDs {
			targets = append(targets, workerTargetDevice{DeviceID: id})
		}
	}
	if len(targets) == 0 {
		return []beam.ProviderDiagnostic{{Provider: providerName, Operation: req.Operation, Status: "skipped", Reason: "no_active_device", CreatedAt: now}}
	}
	diagnostics := make([]beam.ProviderDiagnostic, 0, len(targets))
	for _, target := range targets {
		diagnostics = append(diagnostics, workerDiagnostic(providerName, req.Operation, target, now))
	}
	return diagnostics
}

func workerDiagnostic(providerName, operation string, target workerTargetDevice, now time.Time) beam.ProviderDiagnostic {
	status, reason := "accepted", ""
	if !workerHasToken(operation, target) {
		status, reason = "skipped", "missing_push_token"
	}
	return beam.ProviderDiagnostic{Provider: providerName, Operation: operation, DeviceID: target.DeviceID, Status: status, Reason: reason, CreatedAt: now}
}

func workerHasToken(operation string, target workerTargetDevice) bool {
	if strings.TrimSpace(target.PushToken) != "" {
		return true
	}
	return operation == "activity_start" && strings.TrimSpace(target.PushToStartToken) != ""
}

func writeWorkerJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
