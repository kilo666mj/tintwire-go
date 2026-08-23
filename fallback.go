package tintwire

import (
	"fmt"
	"strings"
)

type mattermostPayload struct {
	Username    string                 `json:"username,omitempty"`
	Channel     string                 `json:"channel,omitempty"`
	Text        string                 `json:"text,omitempty"`
	Attachments []mattermostAttachment `json:"attachments,omitempty"`
}

type mattermostAttachment struct {
	Color    string            `json:"color,omitempty"`
	Title    string            `json:"title,omitempty"`
	Text     string            `json:"text,omitempty"`
	Fields   []mattermostField `json:"fields,omitempty"`
	ImageURL string            `json:"image_url,omitempty"`
}

type mattermostField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

func mattermostPayloadForCard(card Card) mattermostPayload {
	attachment := mattermostAttachment{
		Color: severityColor(card.Severity), Title: card.Title,
		Text: fallbackDetails(card),
	}
	for _, metric := range card.Metrics {
		attachment.Fields = append(attachment.Fields, mattermostField{
			Title: metric.Label, Value: fmt.Sprint(metric.Value), Short: true,
		})
	}
	for _, field := range card.Fields {
		attachment.Fields = append(attachment.Fields, mattermostField{
			Title: field.Label, Value: field.Value, Short: true,
		})
	}
	if len(card.Images) > 0 {
		attachment.ImageURL = card.Images[0].URL
	}
	attachments := []mattermostAttachment{attachment}
	if len(card.Images) > 1 {
		for _, image := range card.Images[1:] {
			attachments = append(attachments, mattermostAttachment{ImageURL: image.URL})
		}
	}
	username := card.Source
	if strings.TrimSpace(username) == "" {
		username = "Tintwire"
	}
	return mattermostPayload{
		Username: username, Channel: card.Channel, Text: card.Summary,
		Attachments: attachments,
	}
}

func fallbackDetails(card Card) string {
	lines := make([]string, 0, len(card.Badges)+len(card.Links)+len(card.Actions)+min(len(card.Rows), 50)+1)
	if len(card.Badges) > 0 {
		badges := make([]string, 0, len(card.Badges))
		for _, badge := range card.Badges {
			badges = append(badges, badge.Label)
		}
		lines = append(lines, "**"+strings.Join(badges, " · ")+"**")
	}
	for _, link := range card.Links {
		lines = append(lines, fmt.Sprintf("[%s](%s)", link.Label, link.URL))
	}
	for _, action := range card.Actions {
		if action.Type == ActionLink {
			lines = append(lines, fmt.Sprintf("[%s](%s)", action.Label, action.URL))
		}
	}
	const rowLimit = 50
	for _, row := range card.Rows[:min(len(card.Rows), rowLimit)] {
		line := "• " + row.Primary
		if len(row.Tags) > 0 {
			line += " — " + strings.Join(row.Tags, ", ")
		}
		lines = append(lines, line)
	}
	if omitted := len(card.Rows) - rowLimit; omitted > 0 {
		lines = append(lines, fmt.Sprintf("…and %d more", omitted))
	}
	return strings.Join(lines, "\n")
}

func severityColor(severity Severity) string {
	switch severity {
	case SeverityCritical:
		return "danger"
	case SeverityWarning:
		return "warning"
	case SeveritySuccess:
		return "good"
	default:
		return "#3aa3e3"
	}
}
