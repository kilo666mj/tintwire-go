# tintwire-go

`tintwire-go` is the small Go publishing client for Tintwire native cards. It
keeps producer code independent from the Tintwire server implementation and can
use an existing Mattermost incoming webhook strictly as delivery failover.

```sh
go get git.internal/mjohnson/tintwire-go
```

```go
client, err := tintwire.New(
    "https://tintwire.example",
    os.Getenv("TINTWIRE_TOKEN"),
    tintwire.WithMattermostFailover(os.Getenv("MATTERMOST_WEBHOOK_URL")),
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
producer bug.

The package uses only the Go standard library. The default HTTP timeout is 10
seconds; use `WithTimeout` or `WithHTTPClient` when a service needs different
transport behavior.
