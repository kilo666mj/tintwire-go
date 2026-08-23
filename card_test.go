package tintwire

import (
	"encoding/json"
	"testing"
)

func TestCardValidation(t *testing.T) {
	valid := Card{
		Title: "Deploy complete", Summary: "Version 1.2.3 is live",
		Severity: SeveritySuccess, Source: "deploy",
		Metrics: []Metric{{Label: "Duration", Value: 42}},
		Fields:  []Field{{Label: "Region", Value: "eu"}},
		Badges:  []Badge{{Label: "Automated", Tone: ToneInfo}},
		Images:  []Image{{URL: "https://example.com/chart.png", Alt: "Deployment chart"}},
		Links:   []Link{{Label: "Runbook", URL: "https://example.com/runbook"}},
		Rows:    []Row{{Primary: "web01", Tags: []string{"healthy"}, Emphasis: EmphasisStrong}},
		Actions: []Action{{Label: "Retry", Type: ActionHTTP, Target: "deploy", Context: json.RawMessage(`{"id":"42"}`)}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Actions = []Action{{Label: "Unsafe", Type: ActionLink, URL: "javascript:alert(1)"}}
	if err := invalid.Validate(); err == nil {
		t.Fatal("unsafe action URL was accepted")
	}
}

func TestFallbackPreservesUsefulCardContent(t *testing.T) {
	payload := mattermostPayloadForCard(Card{
		Channel: "#ops", Title: "Deploy complete", Summary: "Version 1.2.3 is live",
		Severity: SeveritySuccess, Source: "deploy",
		Metrics: []Metric{{Label: "Duration", Value: 42}},
		Fields:  []Field{{Label: "Region", Value: "eu"}},
		Badges:  []Badge{{Label: "Automated", Tone: ToneInfo}},
		Links:   []Link{{Label: "Runbook", URL: "https://example.com/runbook"}},
		Rows:    []Row{{Primary: "web01", Tags: []string{"healthy"}}},
	})
	if payload.Text != "Version 1.2.3 is live" || payload.Attachments[0].Color != "good" || len(payload.Attachments[0].Fields) != 2 {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Attachments[0].Text == "" {
		t.Fatal("fallback details are empty")
	}
}
