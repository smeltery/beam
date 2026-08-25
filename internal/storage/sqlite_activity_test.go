package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/smeltery/beam/internal/beam"
)

func TestSQLiteStorePersistsActivitiesAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "beam.db")

	store, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	activity, _, err := store.StartActivity("dev_token", beam.ActivityRequest{Title: "Deploy", Status: "Building", Key: "deploy"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateActivity("dev_token", "deploy", beam.ActivityRequest{Status: "Testing"}); err != nil {
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
	got, err := reopened.Activity("dev_token", "deploy")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != activity.ID || got.Sequence != 1 || got.State.Status != "Testing" {
		t.Fatalf("unexpected activity after reopen: %#v", got)
	}
}

func TestSQLiteStoreReplaysActivityIdempotencyAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "beam.db")
	body := `{"title":"Deploy","status":"Building","key":"deploy"}`
	store, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	activity, _, err := store.StartActivity("dev_token", beam.ActivityRequest{Title: "Deploy", Status: "Building", Key: "deploy"}, "deploy-1", body)
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
	replay, idempotent, err := reopened.StartActivity("dev_token", beam.ActivityRequest{Title: "Deploy", Status: "Building", Key: "deploy"}, "deploy-1", body)
	if err != nil {
		t.Fatal(err)
	}
	if !idempotent || replay.ID != activity.ID {
		t.Fatalf("replay = %#v idempotent=%v, want %s", replay, idempotent, activity.ID)
	}
}

func TestSQLiteStorePersistsActivityProviderFailureDiagnostics(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "beam.db")

	store, err := OpenSQLiteWithProvider(ctx, dbPath, failingProvider{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.StartActivity("dev_token", beam.ActivityRequest{Title: "Deploy", Status: "Starting"}, "", ""); !errors.Is(err, beam.ErrProviderFailure) {
		t.Fatalf("err = %v, want provider failure", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	activities, err := reopened.Activities("dev_token")
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 1 || activities[0].Status != "failed" || len(activities[0].ProviderDiagnostics) != 1 {
		t.Fatalf("activities = %#v", activities)
	}
	diagnostic := activities[0].ProviderDiagnostics[0]
	if diagnostic.Status != "failed" || diagnostic.Reason != "provider_failure" {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
}
