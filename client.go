package tintwire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultTimeout = 10 * time.Second

type Destination string

const (
	DestinationTintwire   Destination = "tintwire"
	DestinationMattermost Destination = "mattermost"
)

type Result struct {
	Destination    Destination
	NotificationID string
	// PrimaryError is set when Mattermost accepted a notification after the
	// Tintwire destination exhausted its retryable delivery attempts.
	PrimaryError error
}

type Client struct {
	endpoint          string
	token             string
	mattermostWebhook string
	httpClient        *http.Client
	primaryRetries    int
	primaryRetryDelay time.Duration
}

type clientOptions struct {
	mattermostWebhook string
	httpClient        *http.Client
	timeout           time.Duration
	timeoutSet        bool
	primaryRetries    int
	primaryRetryDelay time.Duration
}

type Option func(*clientOptions) error

// WithMattermostFailover configures a webhook used only when Tintwire delivery
// fails. Invalid cards are rejected locally and never sent to the failover.
func WithMattermostFailover(webhookURL string) Option {
	return func(options *clientOptions) error {
		if strings.TrimSpace(webhookURL) == "" {
			options.mattermostWebhook = ""
			return nil
		}
		if err := validateWebhookURL(webhookURL); err != nil {
			return fmt.Errorf("tintwire: invalid Mattermost failover URL: %w", err)
		}
		options.mattermostWebhook = webhookURL
		return nil
	}
}

// WithHTTPClient injects an HTTP client. The client is cloned and never
// modified by this package.
func WithHTTPClient(client *http.Client) Option {
	return func(options *clientOptions) error {
		if client == nil {
			return errors.New("tintwire: HTTP client cannot be nil")
		}
		options.httpClient = client
		return nil
	}
}

// WithTimeout overrides the default 10-second client timeout.
func WithTimeout(timeout time.Duration) Option {
	return func(options *clientOptions) error {
		if timeout <= 0 {
			return errors.New("tintwire: timeout must be positive")
		}
		options.timeout, options.timeoutSet = timeout, true
		return nil
	}
}

// WithPrimaryRetries retries a retryable Tintwire failure before attempting a
// configured Mattermost fallback. retries counts attempts after the initial
// request. The delay is context-aware and policy failures are never retried.
func WithPrimaryRetries(retries int, delay time.Duration) Option {
	return func(options *clientOptions) error {
		if retries < 0 {
			return errors.New("tintwire: primary retries cannot be negative")
		}
		if retries > 0 && delay <= 0 {
			return errors.New("tintwire: primary retry delay must be positive")
		}
		options.primaryRetries = retries
		options.primaryRetryDelay = delay
		return nil
	}
}

// New creates a publisher for a Tintwire origin and bearer publishing token.
func New(baseURL, token string, options ...Option) (*Client, error) {
	endpoint, err := notificationEndpoint(baseURL)
	if err != nil {
		return nil, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("tintwire: publishing token is required")
	}
	return newClient(endpoint, token, options...)
}

// NewFromWebhook creates a native-card publisher from an existing
// https://host/hooks/token URL, which eases migration from Mattermost clients.
func NewFromWebhook(webhookURL string, options ...Option) (*Client, error) {
	parsed, err := parseHTTPURL(webhookURL)
	if err != nil {
		return nil, fmt.Errorf("tintwire: invalid webhook URL: %w", err)
	}
	const marker = "/hooks/"
	index := strings.Index(parsed.Path, marker)
	if index < 0 {
		return nil, errors.New("tintwire: webhook URL must contain /hooks/<token>")
	}
	token := strings.Trim(parsed.Path[index+len(marker):], "/")
	if token == "" || strings.Contains(token, "/") {
		return nil, errors.New("tintwire: webhook URL must contain one token segment")
	}
	parsed.Path, parsed.RawPath, parsed.RawQuery, parsed.Fragment = "", "", "", ""
	return New(parsed.String(), token, options...)
}

func newClient(endpoint, token string, options ...Option) (*Client, error) {
	configuration := clientOptions{timeout: defaultTimeout}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("tintwire: option cannot be nil")
		}
		if err := option(&configuration); err != nil {
			return nil, err
		}
	}
	client := configuration.httpClient
	if client == nil {
		client = &http.Client{Timeout: configuration.timeout}
	} else {
		client = cloneHTTPClient(client)
		if configuration.timeoutSet {
			client.Timeout = configuration.timeout
		}
	}
	return &Client{
		endpoint: endpoint, token: token,
		mattermostWebhook: configuration.mattermostWebhook,
		httpClient:        client,
		primaryRetries:    configuration.primaryRetries,
		primaryRetryDelay: configuration.primaryRetryDelay,
	}, nil
}

// Publish validates and sends a native card. When configured, Mattermost is
// attempted only after Tintwire delivery fails.
func (client *Client) Publish(ctx context.Context, card Card) (Result, error) {
	if client == nil {
		return Result{}, errors.New("tintwire: client is nil")
	}
	card, err := card.normalized()
	if err != nil {
		return Result{}, err
	}
	body, err := json.Marshal(card)
	if err != nil {
		return Result{}, fmt.Errorf("tintwire: marshal card: %w", err)
	}
	var response struct {
		ID string `json:"id"`
	}
	primaryErr := client.postJSON(ctx, client.endpoint, "Bearer "+client.token, body, &response)
	if primaryErr == nil {
		return Result{Destination: DestinationTintwire, NotificationID: response.ID}, nil
	}
	for retries := 0; retries < client.primaryRetries && shouldFailover(ctx, primaryErr); retries++ {
		timer := time.NewTimer(client.primaryRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Result{}, ctx.Err()
		case <-timer.C:
		}
		response.ID = ""
		primaryErr = client.postJSON(ctx, client.endpoint, "Bearer "+client.token, body, &response)
		if primaryErr == nil {
			return Result{Destination: DestinationTintwire, NotificationID: response.ID}, nil
		}
	}
	if client.mattermostWebhook == "" || !shouldFailover(ctx, primaryErr) {
		return Result{}, primaryErr
	}
	fallbackBody, err := json.Marshal(mattermostPayloadForCard(card))
	if err != nil {
		return Result{}, fmt.Errorf("tintwire: marshal Mattermost failover: %w", err)
	}
	fallbackErr := client.postJSON(ctx, client.mattermostWebhook, "", fallbackBody, nil)
	if fallbackErr != nil {
		return Result{}, &DeliveryError{Tintwire: primaryErr, Mattermost: fallbackErr}
	}
	return Result{Destination: DestinationMattermost, PrimaryError: primaryErr}, nil
}

// Send is a convenience wrapper for callers that do not need delivery metadata.
func (client *Client) Send(ctx context.Context, card Card) error {
	_, err := client.Publish(ctx, card)
	return err
}

type DeliveryError struct {
	Tintwire   error
	Mattermost error
}

func (err *DeliveryError) Error() string {
	return fmt.Sprintf("tintwire delivery failed: %v; Mattermost failover failed: %v", err.Tintwire, err.Mattermost)
}

func (err *DeliveryError) Unwrap() error { return err.Tintwire }

// HTTPError reports a non-success response without exposing the endpoint URL.
type HTTPError struct {
	StatusCode int
	Status     string
	Message    string
}

func (err *HTTPError) Error() string {
	if err.Message == "" {
		return fmt.Sprintf("tintwire: endpoint returned %s", err.Status)
	}
	return fmt.Sprintf("tintwire: endpoint returned %s: %s", err.Status, err.Message)
}

func (client *Client) postJSON(ctx context.Context, endpoint, authorization string, body []byte, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("tintwire: create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return &transportError{cause: err}
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(responseBody))
		if len(message) > 1024 {
			message = message[:1024]
		}
		return &HTTPError{StatusCode: response.StatusCode, Status: response.Status, Message: message}
	}
	// A 2xx means the endpoint accepted the notification. Response-body read or
	// decode failures must not trigger a duplicate delivery to the failover.
	if readErr == nil && destination != nil && len(responseBody) > 0 {
		_ = json.Unmarshal(responseBody, destination)
	}
	return nil
}

// transportError retains an inspectable cause while keeping capability URLs
// embedded by net/http out of logs and user-facing error strings.
type transportError struct{ cause error }

func (err *transportError) Error() string { return "tintwire: HTTP transport failed" }
func (err *transportError) Unwrap() error { return err.cause }

func shouldFailover(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var status *HTTPError
	if errors.As(err, &status) {
		return status.StatusCode == http.StatusRequestTimeout || status.StatusCode == http.StatusTooManyRequests || status.StatusCode >= 500
	}
	var transport *transportError
	return errors.As(err, &transport)
}

func notificationEndpoint(baseURL string) (string, error) {
	parsed, err := parseHTTPURL(baseURL)
	if err != nil {
		return "", fmt.Errorf("tintwire: invalid base URL: %w", err)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("tintwire: base URL must be an origin without a path")
	}
	parsed.Path, parsed.RawPath = "/api/v1/notifications", ""
	return parsed.String(), nil
}

func validateWebhookURL(raw string) error {
	parsed, err := parseHTTPURL(raw)
	if err != nil {
		return err
	}
	if strings.Trim(parsed.Path, "/") == "" {
		return errors.New("webhook path is required")
	}
	return nil
}

func parseHTTPURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("URL must use HTTP or HTTPS and contain no credentials, query, or fragment")
	}
	return parsed, nil
}

func cloneHTTPClient(client *http.Client) *http.Client {
	clone := *client
	return &clone
}
