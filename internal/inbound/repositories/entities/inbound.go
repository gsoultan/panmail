package entities

import (
	"time"
)

type InboundEmail struct {
	ID        string            `json:"id"`
	TenantID  string            `json:"tenant_id"`
	From      string            `json:"from"`
	To        []string          `json:"to"`
	Subject   string            `json:"subject"`
	BodyHTML  string            `json:"body_html"`
	BodyText  string            `json:"body_text"`
	Timestamp time.Time         `json:"timestamp"`
	Headers   map[string]string `json:"headers"`
}
