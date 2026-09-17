package tintwire_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	tintwire "github.com/kilo666mj/tintwire-go"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func ExampleClient_Publish() {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusCreated,
			Status:     "201 Created",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"id":"ntf_example"}`)),
		}, nil
	})}
	client, err := tintwire.New(
		"https://tintwire.example.com",
		"example-token",
		tintwire.WithHTTPClient(httpClient),
	)
	if err != nil {
		panic(err)
	}
	result, err := client.Publish(context.Background(), tintwire.Card{
		Channel:  "operations",
		Title:    "Synthetic check failed",
		Summary:  "The documentation endpoint did not answer.",
		Severity: tintwire.SeverityWarning,
		Source:   "docs-check",
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(result.Destination, result.NotificationID)
	// Output: tintwire ntf_example
}
