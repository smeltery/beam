package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/smeltery/beam/internal/beam"
)

func TestProviderWorkerReturnsTokenSafeDiagnostics(t *testing.T) {
	server := httptest.NewServer(providerWorkerHandler(providerWorkerConfig{ProviderName: "apns-worker", Token: "worker_secret"}))
	defer server.Close()

	body := `{
		"operation":"notification",
		"eventId":"evt_test",
		"notification":{"title":"Deploys","body":"shipped"},
		"devices":[{"deviceId":"dev_ios","pushToken":"notification_secret_123"}],
		"createdAt":"2026-07-29T00:00:00Z"
	}`
	req, err := http.NewRequest(http.MethodPost, server.URL+"/deliver", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer worker_secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, raw.String())
	}
	if strings.Contains(raw.String(), "notification_secret_123") || strings.Contains(raw.String(), "worker_secret") {
		t.Fatalf("worker response leaked secret: %s", raw.String())
	}
	var decoded struct {
		Diagnostics []beam.ProviderDiagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(raw.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v", decoded.Diagnostics)
	}
	diagnostic := decoded.Diagnostics[0]
	if diagnostic.Provider != "apns-worker" || diagnostic.Operation != "notification" || diagnostic.DeviceID != "dev_ios" || diagnostic.Status != "accepted" {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
}

func TestProviderWorkerRequiresBearerToken(t *testing.T) {
	server := httptest.NewServer(providerWorkerHandler(providerWorkerConfig{ProviderName: "apns-worker", Token: "worker_secret"}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/deliver", "application/json", strings.NewReader(`{"operation":"notification"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestProviderWorkerValidatesAPNSMode(t *testing.T) {
	if err := (providerWorkerConfig{Mode: "apns"}).validate(); err == nil {
		t.Fatal("expected missing APNs topic error")
	}
	if err := (providerWorkerConfig{Mode: "apns", APNSTopic: "com.example.Beam", APNSEnvironment: "invalid"}).validate(); err == nil {
		t.Fatal("expected invalid APNs environment error")
	}
	if err := (providerWorkerConfig{
		Mode: "apns", APNSTopic: "com.example.Beam", APNSEnvironment: "production",
		APNSKeyID: "KEY123", APNSTeamID: "TEAM123", APNSPrivateKeyPEM: testAPNSPrivateKeyPEM(t),
	}).validate(); err != nil {
		t.Fatalf("validate err = %v", err)
	}
}

func TestProviderWorkerAPNSModeDeliversWithoutLeakingSecrets(t *testing.T) {
	var gotAPNSRequest *http.Request
	apnsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPNSRequest = r.Clone(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	defer apnsServer.Close()

	cfg := providerWorkerConfig{
		Mode: "apns", ProviderName: "apns-worker", Token: "worker_secret",
		APNSTopic: "com.example.Beam", APNSEnvironment: "sandbox", APNSBaseURL: apnsServer.URL, APNSClient: apnsServer.Client(),
		APNSKeyID: "KEY123", APNSTeamID: "TEAM123", APNSPrivateKeyPEM: testAPNSPrivateKeyPEM(t),
	}
	server := httptest.NewServer(providerWorkerHandler(cfg))
	defer server.Close()

	body := `{"operation":"notification","devices":[{"deviceId":"dev_ios","pushToken":"notification_secret_123"}]}`
	req, err := http.NewRequest(http.MethodPost, server.URL+"/deliver", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer worker_secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, raw.String())
	}
	if gotAPNSRequest == nil {
		t.Fatal("expected APNs request")
	}
	if gotAPNSRequest.URL.Path != "/3/device/notification_secret_123" {
		t.Fatalf("APNs path = %q", gotAPNSRequest.URL.Path)
	}
	if gotAPNSRequest.Header.Get("apns-topic") != "com.example.Beam" || gotAPNSRequest.Header.Get("apns-push-type") != "alert" {
		t.Fatalf("APNs headers = %#v", gotAPNSRequest.Header)
	}
	if !strings.HasPrefix(gotAPNSRequest.Header.Get("authorization"), "bearer ") {
		t.Fatalf("APNs authorization = %q", gotAPNSRequest.Header.Get("authorization"))
	}
	for _, secret := range []string{"worker_secret", "notification_secret_123", "KEY123", "TEAM123"} {
		if strings.Contains(raw.String(), secret) {
			t.Fatalf("worker response leaked %q: %s", secret, raw.String())
		}
	}
}

func TestProviderWorkerExpoModeDeliversWithoutLeakingSecrets(t *testing.T) {
	var gotExpoRequest struct {
		Messages []map[string]any
	}
	expoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotExpoRequest.Messages); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"data":[{"status":"ok","id":"ticket-1"}]}`))
	}))
	defer expoServer.Close()

	cfg := providerWorkerConfig{
		Mode: "expo", ProviderName: "expo-worker", Token: "worker_secret",
		ExpoEndpoint: expoServer.URL, ExpoClient: expoServer.Client(),
	}
	server := httptest.NewServer(providerWorkerHandler(cfg))
	defer server.Close()

	body := `{"operation":"notification","eventId":"evt_test","notification":{"title":"Deploys","body":"shipped"},"devices":[{"deviceId":"dev_ios","pushToken":"ExponentPushToken[secret_123]"}]}`
	req, err := http.NewRequest(http.MethodPost, server.URL+"/deliver", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer worker_secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, raw.String())
	}
	if len(gotExpoRequest.Messages) != 1 {
		t.Fatalf("expo messages = %#v", gotExpoRequest.Messages)
	}
	message := gotExpoRequest.Messages[0]
	if message["to"] != "ExponentPushToken[secret_123]" || message["title"] != "Deploys" || message["body"] != "shipped" {
		t.Fatalf("expo message = %#v", message)
	}
	if strings.Contains(raw.String(), "worker_secret") || strings.Contains(raw.String(), "ExponentPushToken[secret_123]") {
		t.Fatalf("worker response leaked secret: %s", raw.String())
	}
	if !strings.Contains(raw.String(), `"provider":"expo-worker"`) || !strings.Contains(raw.String(), `"status":"accepted"`) {
		t.Fatalf("worker response = %s", raw.String())
	}
}

func TestProviderWorkerSkipsMissingPushToken(t *testing.T) {
	diagnostics := workerDiagnostics("apns-worker", workerDeliveryRequest{
		Operation: "notification",
		Devices:   []workerTargetDevice{{DeviceID: "dev_ios"}},
	})
	if len(diagnostics) != 1 || diagnostics[0].Status != "skipped" || diagnostics[0].Reason != "missing_push_token" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestProviderWorkerAcceptsPushToStartTokenForActivityStart(t *testing.T) {
	diagnostics := workerDiagnostics("apns-worker", workerDeliveryRequest{
		Operation: "activity_start",
		Devices:   []workerTargetDevice{{DeviceID: "dev_ios", PushToStartToken: "activity_secret_123"}},
	})
	if len(diagnostics) != 1 || diagnostics[0].Status != "accepted" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestExpoWorkerMapsTicketErrorsAndUnsupportedOperations(t *testing.T) {
	tickets := []expoTicket{{Status: "error", Message: "bad token"}}
	tickets[0].Details.Error = "DeviceNotRegistered"
	diagnostics := expoDiagnostics("expo-worker", "notification", []workerTargetDevice{{DeviceID: "dev_ios"}}, tickets, time.Now().UTC())
	if len(diagnostics) != 1 || diagnostics[0].Status != "failed" || diagnostics[0].Reason != "DeviceNotRegistered" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}

	unsupported := unsupportedExpoDiagnostics("expo-worker", workerDeliveryRequest{
		Operation: "activity_start",
		Devices:   []workerTargetDevice{{DeviceID: "dev_ios", PushToStartToken: "activity_secret_123"}},
	}, time.Now().UTC())
	if len(unsupported) != 1 || unsupported[0].Status != "skipped" || unsupported[0].Reason != "unsupported_operation" {
		t.Fatalf("unsupported diagnostics = %#v", unsupported)
	}
}

func TestExpoWorkerReportsMissingPushTokens(t *testing.T) {
	diagnostics, err := sendExpoRequests(providerWorkerConfig{Mode: "expo", ProviderName: "expo-worker"}, workerDeliveryRequest{
		Operation: "notification",
		Devices:   []workerTargetDevice{{DeviceID: "dev_ios"}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 || diagnostics[0].Status != "skipped" || diagnostics[0].Reason != "missing_push_token" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}
