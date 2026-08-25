package storage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/smeltery/beam/internal/beam"
)

func TestSQLiteStoreDeliversCallbacksAfterReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "beam.db")
	var gotAuth, gotEventID, gotCorrelationID string
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			EventID string     `json:"eventId"`
			Event   beam.Event `json:"event"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotEventID = body.EventID
		if body.Event.Response != nil {
			gotCorrelationID = body.Event.Response.CorrelationID
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackServer.Close()

	store, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	event, _, err := store.SendNotification("dev_token", beam.NotificationRequest{
		Body: "approve deploy",
		Response: &beam.ResponseRequest{
			Type:          "approval",
			CorrelationID: "deploy-42",
			Callback:      &beam.CallbackRequest{URL: "https://callbacks.example.com/beam", Token: "0123456789abcdef"},
		},
	}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := store.store.Snapshot()
	stored := snapshot.Events[event.ID]
	stored.Response.CallbackURL = callbackServer.URL
	snapshot.Events[event.ID] = stored
	store.store = beam.NewStoreFromSnapshot(snapshot)
	if _, err := store.RespondEvent("dev_token", event.ID, beam.ResponseAnswerRequest{Action: "approve"}); err != nil {
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
	count, err := reopened.DeliverDueCallbacks(ctx, callbackServer.Client(), time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("delivered callbacks = %d, want 1", count)
	}
	if gotAuth != "Bearer 0123456789abcdef" || gotEventID != event.ID || gotCorrelationID != "deploy-42" {
		t.Fatalf("callback got auth=%q eventID=%q correlation=%q", gotAuth, gotEventID, gotCorrelationID)
	}
}

func TestSQLiteStorePersistsFailedCallbackAttempt(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "beam.db")
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "try again", http.StatusBadGateway)
	}))
	defer callbackServer.Close()

	store, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	event, _, err := store.SendNotification("dev_token", beam.NotificationRequest{
		Body: "approve deploy",
		Response: &beam.ResponseRequest{
			Type:     "approval",
			Callback: &beam.CallbackRequest{URL: "https://callbacks.example.com/beam", Token: "0123456789abcdef"},
		},
	}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := store.store.Snapshot()
	stored := snapshot.Events[event.ID]
	stored.Response.CallbackURL = callbackServer.URL
	snapshot.Events[event.ID] = stored
	store.store = beam.NewStoreFromSnapshot(snapshot)
	if _, err := store.RespondEvent("dev_token", event.ID, beam.ResponseAnswerRequest{Action: "approve"}); err != nil {
		t.Fatal(err)
	}
	if count, err := store.DeliverDueCallbacks(ctx, callbackServer.Client(), time.Now().UTC().Add(time.Second)); count != 1 || err == nil {
		t.Fatalf("deliver count=%d err=%v, want one failed attempt", count, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Event("dev_token", event.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempts := got.Response.CallbackAttempts
	if len(attempts) == 0 || attempts[0].Status != "failed" || attempts[0].StatusCode != http.StatusBadGateway || attempts[0].Error == "" {
		t.Fatalf("callback attempts after reopen: %#v", attempts)
	}
}
