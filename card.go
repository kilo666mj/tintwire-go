package tintwire

import (
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"strings"
)

// Severity controls the visual priority of a card.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
	SeveritySuccess  Severity = "success"
)

// Tone controls the color of a badge.
type Tone string

const (
	ToneNeutral  Tone = "neutral"
	ToneInfo     Tone = "info"
	ToneWarning  Tone = "warning"
	ToneCritical Tone = "critical"
	ToneSuccess  Tone = "success"
)

// Emphasis controls the visual weight of a row.
type Emphasis string

const EmphasisStrong Emphasis = "strong"

// ActionType identifies how a card action is handled.
type ActionType string

const (
	ActionLink ActionType = "link"
	ActionHTTP ActionType = "http"
)

// Card is a version 1 Tintwire native notification.
// Version may be left at zero; Publish sets it to 1.
type Card struct {
	Version  int      `json:"version"`
	Channel  string   `json:"channel,omitempty"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Severity Severity `json:"severity"`
	Source   string   `json:"source"`
	Metrics  []Metric `json:"metrics,omitempty"`
	Fields   []Field  `json:"fields,omitempty"`
	Badges   []Badge  `json:"badges,omitempty"`
	Images   []Image  `json:"images,omitempty"`
	Links    []Link   `json:"links,omitempty"`
	Rows     []Row    `json:"rows,omitempty"`
	Actions  []Action `json:"actions,omitempty"`
}

type Metric struct {
	Label string `json:"label"`
	Value any    `json:"value"`
}

type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type Badge struct {
	Label string `json:"label"`
	Tone  Tone   `json:"tone,omitempty"`
}

type Image struct {
	URL string `json:"url"`
	Alt string `json:"alt"`
}

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Row struct {
	Primary  string   `json:"primary"`
	Tags     []string `json:"tags,omitempty"`
	Emphasis Emphasis `json:"emphasis,omitempty"`
}

type Action struct {
	Label   string          `json:"label"`
	Type    ActionType      `json:"type"`
	URL     string          `json:"url,omitempty"`
	Target  string          `json:"target,omitempty"`
	Context json.RawMessage `json:"context,omitempty"`
}

var actionTargetPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// Validate checks the public version 1 card contract before delivery.
func (card Card) Validate() error {
	if card.Version != 0 && card.Version != 1 {
		return errors.New("tintwire: version must be 1")
	}
	if strings.TrimSpace(card.Title) == "" {
		return errors.New("tintwire: title is required")
	}
	if len(card.Title) > 200 || len(card.Summary) > 500 {
		return errors.New("tintwire: title or summary is too long")
	}
	if card.Severity != "" && !oneOf(string(card.Severity), "info", "warning", "critical", "success") {
		return errors.New("tintwire: unsupported severity")
	}
	if len(card.Metrics) > 12 || len(card.Fields) > 24 || len(card.Badges) > 16 || len(card.Images) > 4 || len(card.Links) > 12 || len(card.Rows) > 2000 || len(card.Actions) > 8 {
		return errors.New("tintwire: card component limit exceeded")
	}
	for _, metric := range card.Metrics {
		if strings.TrimSpace(metric.Label) == "" || !validMetricValue(metric.Value) {
			return errors.New("tintwire: invalid metric")
		}
	}
	for _, field := range card.Fields {
		if strings.TrimSpace(field.Label) == "" || len(field.Label) > 100 || len(field.Value) > 1000 {
			return errors.New("tintwire: invalid field")
		}
	}
	for _, badge := range card.Badges {
		if strings.TrimSpace(badge.Label) == "" || len(badge.Label) > 80 || !oneOf(string(badge.Tone), "", "neutral", "info", "warning", "critical", "success") {
			return errors.New("tintwire: invalid badge")
		}
	}
	for _, image := range card.Images {
		if !validHTTPURL(image.URL) || strings.TrimSpace(image.Alt) == "" || len(image.Alt) > 200 {
			return errors.New("tintwire: invalid image")
		}
	}
	for _, link := range card.Links {
		if strings.TrimSpace(link.Label) == "" || len(link.Label) > 100 || !validHTTPURL(link.URL) {
			return errors.New("tintwire: invalid link")
		}
	}
	for _, row := range card.Rows {
		if strings.TrimSpace(row.Primary) == "" || (row.Emphasis != "" && row.Emphasis != EmphasisStrong) || len(row.Tags) > 16 {
			return errors.New("tintwire: invalid row")
		}
	}
	for _, action := range card.Actions {
		if err := validateAction(action); err != nil {
			return err
		}
	}
	return nil
}

func validateAction(action Action) error {
	if strings.TrimSpace(action.Label) == "" {
		return errors.New("tintwire: action label is required")
	}
	switch action.Type {
	case ActionLink:
		if !validHTTPURL(action.URL) || action.Target != "" || len(action.Context) > 0 {
			return errors.New("tintwire: invalid link action")
		}
	case ActionHTTP:
		if !actionTargetPattern.MatchString(action.Target) || action.URL != "" {
			return errors.New("tintwire: invalid HTTP action")
		}
		if len(action.Context) > 16<<10 {
			return errors.New("tintwire: HTTP action context is too large")
		}
		if len(action.Context) > 0 {
			var object map[string]any
			if json.Unmarshal(action.Context, &object) != nil {
				return errors.New("tintwire: HTTP action context must be a JSON object")
			}
		}
	default:
		return errors.New("tintwire: unsupported action type")
	}
	return nil
}

func validMetricValue(value any) bool {
	if value == nil {
		return false
	}
	switch number := value.(type) {
	case string:
		return true
	case json.Number:
		_, err := number.Float64()
		return err == nil
	case float32:
		return !math.IsInf(float64(number), 0) && !math.IsNaN(float64(number))
	case float64:
		return !math.IsInf(number, 0) && !math.IsNaN(number)
	}
	kind := reflect.TypeOf(value).Kind()
	return kind >= reflect.Int && kind <= reflect.Uint64
}

func validHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Host != "" && parsed.User == nil && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

func (card Card) normalized() (Card, error) {
	if err := card.Validate(); err != nil {
		return Card{}, err
	}
	card.Version = 1
	if strings.TrimSpace(card.Channel) != "" && !strings.HasPrefix(strings.TrimSpace(card.Channel), "#") {
		card.Channel = "#" + strings.TrimSpace(card.Channel)
	}
	if len(card.Actions) > 0 {
		for index := range card.Actions {
			if card.Actions[index].Type == ActionHTTP && len(card.Actions[index].Context) == 0 {
				card.Actions[index].Context = json.RawMessage(`{}`)
			}
		}
	}
	return card, nil
}
