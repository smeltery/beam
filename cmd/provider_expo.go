package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/smeltery/beam/internal/beam"
)

const defaultExpoPushEndpoint = "https://exp.host/--/api/v2/push/send"

type expoMessage struct {
	To    string         `json:"to"`
	Title string         `json:"title,omitempty"`
	Body  string         `json:"body,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
}

type expoTicket struct {
	Status  string `json:"status"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
	Details struct {
		Error string `json:"error,omitempty"`
	} `json:"details,omitempty"`
}

type expoPushResponse struct {
	Data []expoTicket `json:"data"`
}

func sendExpoRequests(cfg providerWorkerConfig, req workerDeliveryRequest, now time.Time) ([]beam.ProviderDiagnostic, error) {
	if req.Operation != "notification" {
		return unsupportedExpoDiagnostics(cfg.ProviderName, req, now), nil
	}
	messages, targets, missing := expoMessages(req)
	if len(messages) == 0 {
		return workerDiagnostics(expoProviderName(cfg.ProviderName), req), nil
	}
	body, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest(http.MethodPost, expoEndpoint(cfg), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("accept", "application/json")
	httpReq.Header.Set("content-type", "application/json")
	client := cfg.ExpoClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("expo returned %s", resp.Status)
	}
	var decoded expoPushResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	diagnostics := expoDiagnostics(cfg.ProviderName, req.Operation, targets, decoded.Data, now)
	for _, target := range missing {
		diagnostics = append(diagnostics, workerDiagnostic(expoProviderName(cfg.ProviderName), req.Operation, target, now))
	}
	return diagnostics, nil
}

func expoMessages(req workerDeliveryRequest) ([]expoMessage, []workerTargetDevice, []workerTargetDevice) {
	targets := req.Devices
	if len(targets) == 0 {
		for _, id := range req.DeviceIDs {
			targets = append(targets, workerTargetDevice{DeviceID: id})
		}
	}
	messages := make([]expoMessage, 0, len(targets))
	kept := make([]workerTargetDevice, 0, len(targets))
	missing := make([]workerTargetDevice, 0)
	for _, target := range targets {
		token := strings.TrimSpace(target.PushToken)
		if token == "" {
			missing = append(missing, target)
			continue
		}
		message := expoMessage{To: token}
		if req.Notification != nil {
			message.Title = req.Notification["title"]
			message.Body = req.Notification["body"]
		}
		if req.EventID != "" {
			message.Data = map[string]any{"eventId": req.EventID}
		}
		messages = append(messages, message)
		kept = append(kept, target)
	}
	return messages, kept, missing
}

func expoDiagnostics(providerName, operation string, targets []workerTargetDevice, tickets []expoTicket, now time.Time) []beam.ProviderDiagnostic {
	provider := expoProviderName(providerName)
	diagnostics := make([]beam.ProviderDiagnostic, 0, len(targets))
	for i, target := range targets {
		status, reason := "accepted", ""
		if i >= len(tickets) {
			status, reason = "failed", "missing_expo_ticket"
		} else if strings.ToLower(tickets[i].Status) != "ok" {
			status, reason = "failed", firstNonEmpty(tickets[i].Details.Error, tickets[i].Message, "expo_error")
		}
		diagnostics = append(diagnostics, beam.ProviderDiagnostic{Provider: provider, Operation: operation, DeviceID: target.DeviceID, Status: status, Reason: reason, CreatedAt: now})
	}
	return diagnostics
}

func unsupportedExpoDiagnostics(providerName string, req workerDeliveryRequest, now time.Time) []beam.ProviderDiagnostic {
	provider := expoProviderName(providerName)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if len(req.Devices) == 0 && len(req.DeviceIDs) == 0 {
		return []beam.ProviderDiagnostic{{Provider: provider, Operation: req.Operation, Status: "skipped", Reason: "unsupported_operation", CreatedAt: now}}
	}
	targets := req.Devices
	if len(targets) == 0 {
		for _, id := range req.DeviceIDs {
			targets = append(targets, workerTargetDevice{DeviceID: id})
		}
	}
	diagnostics := make([]beam.ProviderDiagnostic, 0, len(targets))
	for _, target := range targets {
		diagnostics = append(diagnostics, beam.ProviderDiagnostic{Provider: provider, Operation: req.Operation, DeviceID: target.DeviceID, Status: "skipped", Reason: "unsupported_operation", CreatedAt: now})
	}
	return diagnostics
}

func expoEndpoint(cfg providerWorkerConfig) string {
	if strings.TrimSpace(cfg.ExpoEndpoint) != "" {
		return strings.TrimSpace(cfg.ExpoEndpoint)
	}
	return defaultExpoPushEndpoint
}

func expoProviderName(providerName string) string {
	if strings.TrimSpace(providerName) != "" {
		return providerName
	}
	return "expo"
}
