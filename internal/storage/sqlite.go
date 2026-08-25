package storage

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/smeltery/beam/internal/beam"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type SQLiteStore struct {
	db    *sql.DB
	store *beam.Store
}

func (s *SQLiteStore) CreateService(req beam.ServiceCreateRequest) (beam.ServiceCreateResponse, error) {
	resp, err := s.store.CreateService(req)
	if err != nil {
		return resp, err
	}
	return resp, s.persist()
}

func (s *SQLiteStore) Services() []beam.PublicService {
	return s.store.Services()
}

func (s *SQLiteStore) Service(id string) (beam.PublicService, error) {
	return s.store.Service(id)
}

func (s *SQLiteStore) ServiceEvents(id string) ([]beam.Event, error) {
	return s.store.ServiceEvents(id)
}

func (s *SQLiteStore) UpdateService(id string, req beam.ServiceUpdateRequest) (beam.PublicService, error) {
	service, err := s.store.UpdateService(id, req)
	if err != nil {
		return service, err
	}
	return service, s.persist()
}

func (s *SQLiteStore) DeleteService(id string) error {
	if err := s.store.DeleteService(id); err != nil {
		return err
	}
	return s.persist()
}

func (s *SQLiteStore) RotateServiceToken(id string) (beam.ServiceCreateResponse, error) {
	resp, err := s.store.RotateServiceToken(id)
	if err != nil {
		return resp, err
	}
	return resp, s.persist()
}

func (s *SQLiteStore) Devices(serviceID string) ([]beam.PublicDevice, error) {
	return s.store.Devices(serviceID)
}

func (s *SQLiteStore) RegisterDevice(serviceID string, req beam.DeviceRegisterRequest) (beam.PublicDevice, error) {
	device, err := s.store.RegisterDevice(serviceID, req)
	if err != nil {
		return device, err
	}
	return device, s.persist()
}

func (s *SQLiteStore) DeactivateDevice(serviceID, deviceID string) (beam.PublicDevice, error) {
	device, err := s.store.DeactivateDevice(serviceID, deviceID)
	if err != nil {
		return device, err
	}
	return device, s.persist()
}

func OpenSQLite(ctx context.Context, path string) (*SQLiteStore, error) {
	return OpenSQLiteWithProvider(ctx, path, beam.LocalPushProvider{})
}

func OpenSQLiteWithProvider(ctx context.Context, path string, provider beam.PushProvider) (*SQLiteStore, error) {
	if err := ensureParent(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("configuring sqlite journal: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	snapshot, err := loadSnapshot(ctx, db)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteStore{db: db, store: beam.NewStoreFromSnapshotWithProvider(snapshot, provider)}, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) SendNotification(token string, req beam.NotificationRequest, idemKey, fingerprint string) (beam.Event, bool, error) {
	event, idempotent, err := s.store.SendNotification(token, req, idemKey, fingerprint)
	if err != nil {
		if persistErr := s.persistProviderFailure(err); persistErr != nil {
			return event, idempotent, persistErr
		}
		return event, idempotent, err
	}
	return event, idempotent, s.persist()
}

func (s *SQLiteStore) StartAuthDevice(req beam.AuthDeviceRequest, verifyBaseURL string) (beam.AuthDevice, error) {
	device, err := s.store.StartAuthDevice(req, verifyBaseURL)
	if err != nil {
		return device, err
	}
	return device, s.persist()
}

func (s *SQLiteStore) AuthDevices() []beam.PublicAuthDevice {
	return s.store.AuthDevices()
}

func (s *SQLiteStore) ApproveAuthDevice(userCode string) (beam.AuthDevice, error) {
	device, err := s.store.ApproveAuthDevice(userCode)
	if err != nil {
		return device, err
	}
	return device, s.persist()
}

func (s *SQLiteStore) AuthDeviceToken(deviceCode string) (beam.AuthDevice, error) {
	device, err := s.store.AuthDeviceToken(deviceCode)
	if err != nil {
		return device, err
	}
	return device, s.persist()
}

func (s *SQLiteStore) AuthDeviceForToken(token string) (beam.AuthDevice, error) {
	return s.store.AuthDeviceForToken(token)
}

func (s *SQLiteStore) RevokeAuthDevice(deviceCode string) (beam.AuthDevice, error) {
	device, err := s.store.RevokeAuthDevice(deviceCode)
	if err != nil {
		return device, err
	}
	return device, s.persist()
}

func (s *SQLiteStore) RevokeAuthToken(token string) (beam.AuthDevice, error) {
	device, err := s.store.RevokeAuthToken(token)
	if err != nil {
		return device, err
	}
	return device, s.persist()
}

func (s *SQLiteStore) Event(token, id string) (beam.Event, error) {
	event, err := s.store.Event(token, id)
	if err != nil {
		return event, err
	}
	return event, s.persist()
}

func (s *SQLiteStore) CancelEvent(token, id string) (beam.Event, error) {
	event, err := s.store.CancelEvent(token, id)
	if err != nil {
		if errors.Is(err, beam.ErrNotFound) {
			if persistErr := s.persist(); persistErr != nil {
				return event, persistErr
			}
		}
		return event, err
	}
	return event, s.persist()
}

func (s *SQLiteStore) RespondEvent(token, id string, req beam.ResponseAnswerRequest) (beam.Event, error) {
	event, err := s.store.RespondEvent(token, id, req)
	if err != nil {
		if errors.Is(err, beam.ErrNotFound) {
			if persistErr := s.persist(); persistErr != nil {
				return event, persistErr
			}
		}
		return event, err
	}
	return event, s.persist()
}

func (s *SQLiteStore) DeliverDueCallbacks(ctx context.Context, client *http.Client, now time.Time) (int, error) {
	count, err := s.store.DeliverDueCallbacks(ctx, client, now)
	if count == 0 {
		return count, err
	}
	if persistErr := s.persist(); persistErr != nil {
		return count, persistErr
	}
	return count, err
}

func (s *SQLiteStore) StartActivity(token string, req beam.ActivityRequest, idemKey, fingerprint string) (beam.Activity, bool, error) {
	activity, idempotent, err := s.store.StartActivity(token, req, idemKey, fingerprint)
	if err != nil {
		if persistErr := s.persistProviderFailure(err); persistErr != nil {
			return activity, idempotent, persistErr
		}
		return activity, idempotent, err
	}
	return activity, idempotent, s.persist()
}

func (s *SQLiteStore) Activity(token, id string) (beam.Activity, error) {
	activity, err := s.store.Activity(token, id)
	if err != nil {
		return activity, err
	}
	return activity, s.persist()
}

func (s *SQLiteStore) Activities(token string) ([]beam.Activity, error) {
	activities, err := s.store.Activities(token)
	if err != nil {
		return activities, err
	}
	return activities, s.persist()
}

func (s *SQLiteStore) UpdateActivity(token, id string, req beam.ActivityRequest) (beam.Activity, error) {
	activity, err := s.store.UpdateActivity(token, id, req)
	if err != nil {
		if persistErr := s.persistProviderFailure(err); persistErr != nil {
			return activity, persistErr
		}
		return activity, err
	}
	return activity, s.persist()
}

func (s *SQLiteStore) EndActivity(token, id string, req beam.ActivityRequest) (beam.Activity, error) {
	activity, err := s.store.EndActivity(token, id, req)
	if err != nil {
		if persistErr := s.persistProviderFailure(err); persistErr != nil {
			return activity, persistErr
		}
		return activity, err
	}
	return activity, s.persist()
}

func (s *SQLiteStore) persistProviderFailure(err error) error {
	if errors.Is(err, beam.ErrProviderFailure) {
		return s.persist()
	}
	return nil
}

func (s *SQLiteStore) persist() error {
	payload, err := json.Marshal(s.store.Snapshot())
	if err != nil {
		return fmt.Errorf("marshaling store snapshot: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO snapshots (name, payload, updated_at)
		 VALUES ('beam', ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(name) DO UPDATE SET payload = excluded.payload, updated_at = CURRENT_TIMESTAMP`,
		string(payload),
	)
	if err != nil {
		return fmt.Errorf("persisting store snapshot: %w", err)
	}
	return nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("reading migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		name := entry.Name()
		version, ok := migrationVersion(name)
		if !ok {
			continue
		}
		applied, err := migrationApplied(ctx, db, version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		sqlText, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", name, err)
		}
		if _, err := db.ExecContext(ctx, string(sqlText)); err != nil {
			return fmt.Errorf("applying migration %s: %w", name, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations (version) VALUES (?)`, version); err != nil {
			return fmt.Errorf("recording migration %s: %w", name, err)
		}
	}
	return nil
}

func migrationApplied(ctx context.Context, db *sql.DB, version int) (bool, error) {
	var exists int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE version = ?`, version).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows || strings.Contains(err.Error(), "no such table") {
		return false, nil
	}
	return false, fmt.Errorf("checking migration %d: %w", version, err)
}

func migrationVersion(name string) (int, bool) {
	var version int
	if _, err := fmt.Sscanf(name, "%d_", &version); err != nil {
		return 0, false
	}
	return version, true
}

func loadSnapshot(ctx context.Context, db *sql.DB) (beam.Snapshot, error) {
	var payload string
	err := db.QueryRowContext(ctx, `SELECT payload FROM snapshots WHERE name = 'beam'`).Scan(&payload)
	if err == sql.ErrNoRows {
		return beam.NewStore().Snapshot(), nil
	}
	if err != nil {
		return beam.Snapshot{}, fmt.Errorf("loading store snapshot: %w", err)
	}
	var snapshot beam.Snapshot
	if err := json.Unmarshal([]byte(payload), &snapshot); err != nil {
		return beam.Snapshot{}, fmt.Errorf("parsing store snapshot: %w", err)
	}
	return snapshot, nil
}

func ensureParent(path string) error {
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return mkdirAll(dir)
}
