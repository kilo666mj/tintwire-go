package tintwire

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func testResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status),
		Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)),
	}
}

func TestPublishUsesNativeCardAndReturnsID(t *testing.T) {
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.String() != "https://tintwire.example/api/v1/notifications" {
			t.Fatalf("URL = %s", request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer publishing-token" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		var card Card
		if err := json.NewDecoder(request.Body).Decode(&card); err != nil {
			t.Fatal(err)
		}
		if card.Version != 1 || card.Channel != "#logw" || card.Title != "Disk full" || card.Summary != "web01 is at 95%" {
			t.Fatalf("card = %#v", card)
		}
		return testResponse(http.StatusCreated, `{"id":"ntf_example"}`), nil
	})}
	client, err := New("https://tintwire.example", "publishing-token", WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Publish(context.Background(), Card{
		Channel: "logw", Title: "Disk full", Summary: "web01 is at 95%",
		Severity: SeverityCritical, Source: "monitor",
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || result.Destination != DestinationTintwire || result.NotificationID != "ntf_example" {
		t.Fatalf("requests = %d, result = %#v", requests, result)
	}
}

func TestPublishPrimaryDoesNotRepeatFallback(t *testing.T) {
	primaryStatus := http.StatusServiceUnavailable
	primaryCalls, fallbackCalls := 0, 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "mattermost.example" {
			fallbackCalls++
			return testResponse(http.StatusOK, ""), nil
		}
		primaryCalls++
		return testResponse(primaryStatus, `{"id":"ntf_recovered"}`), nil
	})}
	client, err := New("https://tintwire.example", "token", WithHTTPClient(httpClient),
		WithMattermostFailover("https://mattermost.example/hooks/fallback"), WithPrimaryRetries(1, time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	card := Card{Title: "Recovered", State: StateResolved, LifecycleKey: "test/service"}
	if result, err := client.Publish(t.Context(), card); err != nil || result.Destination != DestinationMattermost {
		t.Fatalf("initial publish = %+v, %v", result, err)
	}
	if _, err := client.PublishPrimary(t.Context(), card); err == nil {
		t.Fatal("primary-only retry hid primary failure")
	}
	if primaryCalls != 4 || fallbackCalls != 1 {
		t.Fatalf("attempts: primary=%d fallback=%d", primaryCalls, fallbackCalls)
	}
	primaryStatus = http.StatusCreated
	if result, err := client.PublishPrimary(t.Context(), card); err != nil || result.NotificationID != "ntf_recovered" || result.Destination != DestinationTintwire {
		t.Fatalf("primary recovery = %+v, %v", result, err)
	}
	if fallbackCalls != 1 {
		t.Fatalf("repeated fallback %d times", fallbackCalls)
	}
	if _, err := client.PublishPrimary(t.Context(), Card{}); err == nil || primaryCalls != 5 {
		t.Fatalf("invalid card was not rejected locally: %v, calls=%d", err, primaryCalls)
	}
	var nilClient *Client
	if _, err := nilClient.PublishPrimary(t.Context(), card); err == nil {
		t.Fatal("nil client accepted")
	}
}

func TestMattermostIsFailoverNotDualDelivery(t *testing.T) {
	for _, test := range []struct {
		name            string
		primaryStatus   int
		wantRequests    int
		wantDestination Destination
	}{
		{name: "native success", primaryStatus: http.StatusCreated, wantRequests: 1, wantDestination: DestinationTintwire},
		{name: "native failure", primaryStatus: http.StatusServiceUnavailable, wantRequests: 2, wantDestination: DestinationMattermost},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				if requests == 1 {
					return testResponse(test.primaryStatus, `{"id":"ntf_native"}`), nil
				}
				if request.URL.String() != "https://matter.example/hooks/fallback" || request.Header.Get("Authorization") != "" {
					t.Fatalf("failover request = %s, authorization = %q", request.URL, request.Header.Get("Authorization"))
				}
				var payload mattermostPayload
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if payload.Channel != "#alerts" || payload.Username != "watcher" || payload.Text != "The details" || payload.Attachments[0].Title != "Alert title" {
					t.Fatalf("payload = %#v", payload)
				}
				return testResponse(http.StatusOK, "ok"), nil
			})}
			client, err := New("https://tintwire.example", "token",
				WithMattermostFailover("https://matter.example/hooks/fallback"), WithHTTPClient(httpClient))
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Publish(context.Background(), Card{Channel: "#alerts", Title: "Alert title", Summary: "The details", Source: "watcher"})
			if err != nil {
				t.Fatal(err)
			}
			if requests != test.wantRequests || result.Destination != test.wantDestination {
				t.Fatalf("requests = %d, result = %#v", requests, result)
			}
			if test.wantDestination == DestinationMattermost && result.PrimaryError == nil {
				t.Fatalf("fallback result did not retain primary error: %#v", result)
			}
		})
	}
}

func TestPrimaryRetriesBeforeFailover(t *testing.T) {
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests < 3 {
			return testResponse(http.StatusServiceUnavailable, "try again"), nil
		}
		return testResponse(http.StatusCreated, `{"id":"ntf_retry"}`), nil
	})}
	client, err := New("https://tintwire.example", "token",
		WithMattermostFailover("https://matter.example/hooks/fallback"),
		WithPrimaryRetries(2, time.Nanosecond), WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Publish(context.Background(), Card{Title: "Retry me"})
	if err != nil || requests != 3 || result.Destination != DestinationTintwire || result.NotificationID != "ntf_retry" {
		t.Fatalf("requests = %d, result = %#v, error = %v", requests, result, err)
	}
}

func TestPrimaryRetryOptionValidation(t *testing.T) {
	for _, option := range []Option{WithPrimaryRetries(-1, time.Second), WithPrimaryRetries(1, 0)} {
		if _, err := New("https://tintwire.example", "token", option); err == nil {
			t.Fatal("invalid primary retry option was accepted")
		}
	}
}

func TestPolicyRejectionDoesNotFailOver(t *testing.T) {
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		return testResponse(http.StatusForbidden, "channel override denied"), nil
	})}
	client, err := New("https://tintwire.example", "token",
		WithMattermostFailover("https://matter.example/hooks/fallback"), WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	err = client.Send(context.Background(), Card{Title: "Denied", Channel: "#private"})
	var status *HTTPError
	if !errors.As(err, &status) || status.StatusCode != http.StatusForbidden {
		t.Fatalf("error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("policy rejection made %d requests", requests)
	}
}

func TestAcceptedMalformedResponseDoesNotDuplicate(t *testing.T) {
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		return testResponse(http.StatusCreated, "not-json"), nil
	})}
	client, err := New("https://tintwire.example", "token",
		WithMattermostFailover("https://matter.example/hooks/fallback"), WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Publish(context.Background(), Card{Title: "Accepted"})
	if err != nil || result.Destination != DestinationTintwire || requests != 1 {
		t.Fatalf("result = %#v, requests = %d, error = %v", result, requests, err)
	}
}

func TestNewFromWebhookDerivesOriginAndToken(t *testing.T) {
	var got *http.Request
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		got = request
		return testResponse(http.StatusCreated, `{"id":"ntf_1"}`), nil
	})}
	client, err := NewFromWebhook("https://tintwire.example/hooks/secret-token", WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Send(context.Background(), Card{Title: "Hello"}); err != nil {
		t.Fatal(err)
	}
	if got.URL.Path != "/api/v1/notifications" || got.Header.Get("Authorization") != "Bearer secret-token" {
		t.Fatalf("request = %s, authorization = %q", got.URL, got.Header.Get("Authorization"))
	}
}

func TestInvalidCardDoesNotFailOver(t *testing.T) {
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		return testResponse(http.StatusOK, "ok"), nil
	})}
	client, err := New("https://tintwire.example", "token",
		WithMattermostFailover("https://matter.example/hooks/fallback"), WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Send(context.Background(), Card{Summary: "missing title"}); err == nil {
		t.Fatal("invalid card was accepted")
	}
	if requests != 0 {
		t.Fatalf("invalid card made %d requests", requests)
	}
}

func TestDeliveryErrorDoesNotExposeToken(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Post", URL: request.URL.String(), Err: io.ErrUnexpectedEOF}
	})}
	client, err := NewFromWebhook(
		"https://tintwire.example/hooks/very-secret-primary-token",
		WithMattermostFailover("https://matter.example/hooks/very-secret-failover-token"),
		WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	err = client.Send(context.Background(), Card{Title: "Hello"})
	if err == nil || strings.Contains(err.Error(), "very-secret-primary-token") || strings.Contains(err.Error(), "very-secret-failover-token") {
		t.Fatalf("error = %v", err)
	}
}

func TestEmptyMattermostFailoverIsDisabled(t *testing.T) {
	client, err := New("https://tintwire.example", "token", WithMattermostFailover(""))
	if err != nil || client.mattermostWebhook != "" {
		t.Fatalf("client = %#v, error = %v", client, err)
	}
}
