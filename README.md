# tintwire-go

`tintwire-go` is the small Go publishing client for Tintwire native cards. It
keeps producer code independent from the Tintwire server implementation and can
use an existing Mattermost incoming webhook strictly as delivery failover.

```sh
go get github.com/kilo666mj/tintwire-go@v0.1.0
```

Pin `@v0.1.0` for reproducible builds. `tintwire-go` requires Go 1.24 or newer,
uses only the standard library, and is tested at both the minimum and current Go
releases. See the complete API on
[pkg.go.dev](https://pkg.go.dev/github.com/kilo666mj/tintwire-go).

```go
client, err := tintwire.New(
    "https://tintwire.example",
    os.Getenv("TINTWIRE_TOKEN"),
    tintwire.WithMattermostFailover(os.Getenv("MATTERMOST_WEBHOOK_URL")),
    tintwire.WithPrimaryRetries(2, 250*time.Millisecond),
)
if err != nil {
    log.Fatal(err)
}

result, err := client.Publish(context.Background(), tintwire.Card{
    Channel:  "#logw",
    Title:    "rsyslogd alert on fleeb",
    Summary:  "The remote log server is unavailable.",
    Severity: tintwire.SeverityWarning,
    Source:   "log_watcher",
    Fields: []tintwire.Field{
        {Label: "Host", Value: "fleeb"},
        {Label: "Process", Value: "rsyslogd"},
    },
})
if err != nil {
    log.Printf("notification failed: %v", err)
} else {
    log.Printf("notification delivered by %s", result.Destination)
	if result.PrimaryError != nil {
		log.Printf("Tintwire failed before fallback: %v", result.PrimaryError)
	}
}
```

For services that already store a Mattermost-compatible Tintwire hook URL,
`NewFromWebhook` derives the native endpoint and bearer token:

```go
client, err := tintwire.NewFromWebhook(
    os.Getenv("TINTWIRE_WEBHOOK_URL"),
    tintwire.WithMattermostFailover(os.Getenv("MATTERMOST_BACKUP_WEBHOOK_URL")),
)
```

Mattermost is never dual-published. Failover is attempted only for transport
failures, HTTP 408/429 responses, and server-side 5xx responses. Authentication,
authorization, channel-policy, and payload rejections do not fail over. Invalid
cards are rejected locally because a second representation would hide a
producer bug. `WithPrimaryRetries` adds a bounded, context-aware delay before
fallback; it is opt-in so existing clients retain their delivery timing.

The package uses only the Go standard library. The default HTTP timeout is 10
seconds; use `WithTimeout` or `WithHTTPClient` when a service needs different
transport behavior.

## Ownership boundaries

The library owns version-1 card validation and encoding, bounded HTTP response
handling, transport-safe errors, native delivery, and the optional failover
decision. The producer owns token storage, channel and recipient policy,
payload sensitivity, deadlines, retry/failover configuration, logging, metrics,
and the decision to retry after an ambiguous transport failure. The Tintwire
server remains the authority for authentication and channel authorization.

## Adoption checklist

1. Pin the module and construct one long-lived client with a secret publishing
   token supplied by the deployment secret store.
2. Set an application deadline and decide whether the default transport timeout
   fits it; inject an existing instrumented `http.Client` when appropriate.
3. Start with native delivery only. Configure Mattermost failover only for an
   independently operated recovery path.
4. Send stable `Source`, `Channel`, and field labels; keep secrets and unbounded
   diagnostic blobs out of cards.
5. Record `Result.Destination` and `PrimaryError` without logging tokens or
   capability URLs. Test 4xx, 429, 5xx, timeout, cancellation, and fallback.
