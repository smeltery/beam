package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/smeltery/beam/internal/config"
)

func apiClient() (*config.Config, *http.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	if apiURL := strings.TrimSpace(os.Getenv("BEAM_API_URL")); apiURL != "" {
		cfg.APIURL = apiURL
	}
	if token := strings.TrimSpace(os.Getenv("BEAM_TOKEN")); token != "" {
		cfg.Token = token
	}
	return cfg, &http.Client{
		Timeout:   15 * time.Second,
		Transport: authTransport{token: cfg.Token, base: http.DefaultTransport},
	}, nil
}

type authTransport struct {
	token string
	base  http.RoundTripper
}

func (t authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.TrimSpace(t.token) != "" && req.Header.Get("Authorization") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(t.token))
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

type APIError struct {
	StatusCode int
	Status     string
	Body       string
}

func (err APIError) Error() string {
	if err.Body == "" {
		return fmt.Sprintf("api returned %s", err.Status)
	}
	return fmt.Sprintf("api returned %s: %s", err.Status, err.Body)
}

type NetworkError struct {
	Err error
}

func (err NetworkError) Error() string {
	return err.Err.Error()
}

func (err NetworkError) Unwrap() error {
	return err.Err
}

var ErrNoDeviceAccepted = errors.New("no device accepted push")
var ErrInteractionTimedOut = errors.New("interaction timed out")
var ErrInteractionUnavailable = errors.New("interaction expired or canceled")
var ErrInteractionDenied = errors.New("interaction denied")

type UsageError struct {
	Err error
}

func (err UsageError) Error() string {
	return err.Err.Error()
}

func (err UsageError) Unwrap() error {
	return err.Err
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var usageErr UsageError
	if errors.As(err, &usageErr) {
		return 2
	}
	if errors.Is(err, ErrNoDeviceAccepted) {
		return 7
	}
	if errors.Is(err, ErrInteractionTimedOut) || errors.Is(err, ErrInteractionUnavailable) {
		return 4
	}
	if errors.Is(err, ErrInteractionDenied) {
		return 5
	}

	var apiErr APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden {
			return 3
		}
		return 1
	}

	var networkErr NetworkError
	if errors.As(err, &networkErr) {
		return 6
	}

	return 1
}

func postJSON(client *http.Client, url string, payload any, idempotencyKey string) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	return do(req, client)
}

func patchJSON(client *http.Client, url string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return do(req, client)
}

func deleteJSON(client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, err
	}
	return do(req, client)
}

func getJSON(client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return do(req, client)
}

func do(req *http.Request, client *http.Client) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, NetworkError{Err: err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return data, APIError{StatusCode: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(data))}
	}
	return data, nil
}

func hookURL(cfg *config.Config, suffix string) string {
	return strings.TrimRight(cfg.APIURL, "/") + "/hooks/" + cfg.Token + suffix
}

func apiURL(cfg *config.Config, suffix string) string {
	return strings.TrimRight(cfg.APIURL, "/") + suffix
}
