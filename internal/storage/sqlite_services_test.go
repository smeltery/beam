package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smeltery/beam/internal/beam"
)

func TestSQLiteStorePersistsServiceLifecycle(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "beam.db")

	store, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateService(beam.ServiceCreateRequest{Title: "CI"})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := store.RotateServiceToken(created.Service.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	service, err := reopened.Service(created.Service.ID)
	if err != nil {
		t.Fatal(err)
	}
	if service.Title != "CI" {
		t.Fatalf("unexpected service after reopen: %#v", service)
	}
	if _, _, err := reopened.SendNotification(created.Token, beam.NotificationRequest{Body: "old"}, "", ""); err != beam.ErrUnknownWebhook {
		t.Fatalf("expected old token to be revoked, got %v", err)
	}
	if _, _, err := reopened.SendNotification(rotated.Token, beam.NotificationRequest{Body: "new"}, "", ""); err != nil {
		t.Fatal(err)
	}

	payload := snapshotPayload(t, dbPath)
	if strings.Contains(payload, created.Token) || strings.Contains(payload, rotated.Token) {
		t.Fatalf("snapshot contains plaintext service token: %s", payload)
	}
	if !strings.Contains(payload, `"tokenHash"`) {
		t.Fatalf("snapshot is missing token hash: %s", payload)
	}
}

func TestSQLiteStorePersistsDevices(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "beam.db")

	store, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateService(beam.ServiceCreateRequest{Title: "CI"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.RegisterDevice(created.Service.ID, beam.DeviceRegisterRequest{
		Name:             "Nick's iPhone",
		Platform:         "ios",
		PushToken:        "notification_secret_123",
		PushToStartToken: "0123456789abcdef",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !device.PushTokenRegistered || !device.PushToStartTokenRegistered {
		t.Fatalf("push token registration missing: %#v", device)
	}
	if _, err := store.DeactivateDevice(created.Service.ID, device.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	devices, err := reopened.Devices(created.Service.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].ID != device.ID || devices[0].Active || !devices[0].PushTokenRegistered || !devices[0].PushToStartTokenRegistered {
		t.Fatalf("unexpected devices after reopen: %#v", devices)
	}
}

func TestSQLiteStorePersistsAuthConnectionRevocation(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "beam.db")

	store, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.StartAuthDevice(beam.AuthDeviceRequest{ClientName: "Agent", Scopes: []string{"notify"}}, "https://beam.example.com")
	if err != nil {
		t.Fatal(err)
	}
	approved, err := store.ApproveAuthDevice(device.UserCode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RevokeAuthDevice(approved.DeviceCode); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	connections := reopened.AuthDevices()
	if len(connections) != 1 || connections[0].Status != "revoked" {
		t.Fatalf("connections after reopen: %#v", connections)
	}
	if tokenDevice, err := reopened.AuthDeviceToken(approved.DeviceCode); err != nil || tokenDevice.Token != "" {
		t.Fatalf("unexpected token after reopen: %#v err=%v", tokenDevice, err)
	}
}

func TestSQLiteStorePersistsAgentTokenHashOnly(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "beam.db")

	store, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.StartAuthDevice(beam.AuthDeviceRequest{ClientName: "Agent", Scopes: []string{"services"}}, "https://beam.example.com")
	if err != nil {
		t.Fatal(err)
	}
	approved, err := store.ApproveAuthDevice(device.UserCode)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Token == "" || approved.TokenHash == "" {
		t.Fatalf("approved device missing token material: %#v", approved)
	}
	token := approved.Token
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	payload := snapshotPayload(t, dbPath)
	if strings.Contains(payload, token) || strings.Contains(payload, `"token":"beam_agent_`) {
		t.Fatalf("snapshot contains plaintext agent token: %s", payload)
	}
	if !strings.Contains(payload, `"tokenHash"`) {
		t.Fatalf("snapshot is missing agent token hash: %s", payload)
	}

	reopened, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.AuthDeviceForToken(token); err != nil {
		t.Fatalf("hashed token was not accepted after reopen: %v", err)
	}
	tokenDevice, err := reopened.AuthDeviceToken(approved.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if tokenDevice.Token != "" || tokenDevice.TokenHash == "" || tokenDevice.Status != "approved" {
		t.Fatalf("unexpected token response state after reopen: %#v", tokenDevice)
	}
}

func TestSQLiteStoreUsesServiceNotificationDefaults(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "beam.db")
	provider := &capturingProvider{}

	store, err := OpenSQLiteWithProvider(ctx, dbPath, provider)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateService(beam.ServiceCreateRequest{
		Title:    "Deploys",
		ImageURL: "https://assets.example.com/beam.png",
		URL:      "https://ci.example.com/builds/42",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.SendNotification(created.Token, beam.NotificationRequest{Body: "build passed"}, "", ""); err != nil {
		t.Fatal(err)
	}

	if provider.notification.Title != "Deploys" || provider.notification.ImageURL != "https://assets.example.com/beam.png" || provider.notification.URL != "https://ci.example.com/builds/42" {
		t.Fatalf("notification defaults = %#v", provider.notification)
	}
}

func TestSQLiteStorePersistsSharedAccountMonthlyAllowance(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "beam.db")

	store, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	registerAccountLimitedService(store, "svc_account_a", "account_a_token")
	registerAccountLimitedService(store, "svc_account_b", "account_b_token")
	if err := store.persist(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.SendNotification("account_a_token", beam.NotificationRequest{Body: "first"}, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, _, err := reopened.SendNotification("account_b_token", beam.NotificationRequest{Body: "second"}, "", ""); !errors.Is(err, beam.ErrAllowanceExceeded) {
		t.Fatalf("err = %v, want allowance exceeded", err)
	}
}

type capturingProvider struct {
	notification beam.PushNotification
}

func (p *capturingProvider) SendNotification(req beam.PushNotification) ([]beam.ProviderDiagnostic, error) {
	p.notification = req
	return beam.LocalPushProvider{}.SendNotification(req)
}

func (p *capturingProvider) StartActivity(req beam.ActivityPush) ([]beam.ProviderDiagnostic, error) {
	return beam.LocalPushProvider{}.StartActivity(req)
}

func (p *capturingProvider) UpdateActivity(req beam.ActivityPush) ([]beam.ProviderDiagnostic, error) {
	return beam.LocalPushProvider{}.UpdateActivity(req)
}

func (p *capturingProvider) EndActivity(req beam.ActivityPush) ([]beam.ProviderDiagnostic, error) {
	return beam.LocalPushProvider{}.EndActivity(req)
}

func registerAccountLimitedService(store *SQLiteStore, id, token string) {
	limits := beam.ServiceLimits{
		RequestsPerMinute: 10,
		MonthlyOperations: 1,
		DeviceRouting:     true,
	}
	store.store.RegisterService(beam.Service{
		ID:            id,
		AccountID:     "acct_shared",
		Token:         token,
		Title:         id,
		Limits:        beam.ServiceLimits{RequestsPerMinute: 10, MonthlyOperations: 10, DeviceRouting: true},
		AccountLimits: limits,
	})
}

func snapshotPayload(t *testing.T, dbPath string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var payload string
	if err := db.QueryRow(`SELECT payload FROM snapshots WHERE name = 'beam'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}
