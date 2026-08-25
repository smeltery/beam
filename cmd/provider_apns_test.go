package cmd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/smeltery/beam/internal/beam"
)

func TestAPNSRequestsBuildNotificationRequest(t *testing.T) {
	reqs, err := apnsRequests(providerWorkerConfig{
		APNSTopic: "com.example.Beam", APNSEnvironment: "sandbox",
	}, workerDeliveryRequest{
		Operation:    "notification",
		Notification: map[string]string{"title": "Deploys", "body": "shipped"},
		Devices:      []workerTargetDevice{{DeviceID: "dev_ios", PushToken: "notification_secret_123"}},
	}, "jwt_secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 {
		t.Fatalf("requests = %#v", reqs)
	}
	got := reqs[0]
	if got.URL != "https://api.sandbox.push.apple.com/3/device/notification_secret_123" {
		t.Fatalf("url = %q", got.URL)
	}
	if got.Headers.Get("apns-topic") != "com.example.Beam" || got.Headers.Get("apns-push-type") != "alert" {
		t.Fatalf("headers = %#v", got.Headers)
	}
	if got.Headers.Get("authorization") != "bearer jwt_secret" {
		t.Fatalf("authorization = %q", got.Headers.Get("authorization"))
	}
	var body map[string]any
	if err := json.Unmarshal(got.Body, &body); err != nil {
		t.Fatal(err)
	}
	aps := body["aps"].(map[string]any)
	alert := aps["alert"].(map[string]any)
	if alert["title"] != "Deploys" || alert["body"] != "shipped" {
		t.Fatalf("body = %s", got.Body)
	}
}

func TestAPNSBearerTokenSignsES256JWT(t *testing.T) {
	token, err := apnsBearerToken(providerWorkerConfig{
		APNSKeyID: "KEY123", APNSTeamID: "TEAM123", APNSPrivateKeyPEM: testAPNSPrivateKeyPEM(t),
	}, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token parts = %d", len(parts))
	}
	header := decodeJWTPart[map[string]string](t, parts[0])
	if header["alg"] != "ES256" || header["kid"] != "KEY123" {
		t.Fatalf("header = %#v", header)
	}
	claims := decodeJWTPart[map[string]any](t, parts[1])
	if claims["iss"] != "TEAM123" || int64(claims["iat"].(float64)) != 1700000000 {
		t.Fatalf("claims = %#v", claims)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	if len(signature) != 64 {
		t.Fatalf("signature len = %d", len(signature))
	}
}

func decodeJWTPart[T any](t *testing.T, part string) T {
	t.Helper()
	data, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func testAPNSPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: data}))
}

func TestAPNSRequestsUsePushToStartTokenForActivityStart(t *testing.T) {
	reqs, err := apnsRequests(providerWorkerConfig{
		APNSTopic: "com.example.Beam.push-type.liveactivity", APNSEnvironment: "production",
	}, workerDeliveryRequest{
		Operation: "activity_start",
		Activity: &struct {
			State    beam.ActivityState `json:"state"`
			Sequence int                `json:"sequence"`
		}{State: beam.ActivityState{Title: "Deploy", Status: "Running"}, Sequence: 0},
		Devices: []workerTargetDevice{{DeviceID: "dev_ios", PushToken: "notification_secret_123", PushToStartToken: "activity_secret_123"}},
	}, "jwt_secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 {
		t.Fatalf("requests = %#v", reqs)
	}
	if !strings.Contains(reqs[0].URL, "/3/device/activity_secret_123") {
		t.Fatalf("url = %q", reqs[0].URL)
	}
	if reqs[0].Headers.Get("apns-push-type") != "liveactivity" {
		t.Fatalf("headers = %#v", reqs[0].Headers)
	}
	if !strings.Contains(string(reqs[0].Body), `"beam-sequence":0`) {
		t.Fatalf("body = %s", reqs[0].Body)
	}
}

func TestSendAPNSRequestsRecordsProviderRejection(t *testing.T) {
	apnsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/device/notification_secret_123" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("authorization") != "bearer jwt_secret" {
			t.Fatalf("authorization = %q", r.Header.Get("authorization"))
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"reason":"BadDeviceToken"}`))
	}))
	defer apnsServer.Close()

	reqs, err := apnsRequests(providerWorkerConfig{
		APNSTopic: "com.example.Beam", APNSBaseURL: apnsServer.URL,
	}, workerDeliveryRequest{
		Operation: "notification",
		Devices:   []workerTargetDevice{{DeviceID: "dev_ios", PushToken: "notification_secret_123"}},
	}, "jwt_secret")
	if err != nil {
		t.Fatal(err)
	}
	diagnostics, err := sendAPNSRequests(providerWorkerConfig{
		ProviderName: "apns-worker", APNSClient: apnsServer.Client(),
	}, "notification", reqs, time.Unix(1700000000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	got := diagnostics[0]
	if got.Provider != "apns-worker" || got.DeviceID != "dev_ios" || got.Status != "failed" || got.Reason != "apns_status_400" {
		t.Fatalf("diagnostic = %#v", got)
	}
}
