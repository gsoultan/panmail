package entities

import (
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"time"
)

type EmailEvent struct {
	ID           string                   `json:"id"`
	TenantID     string                   `json:"tenant_id"`
	ProviderID   string                   `json:"provider_id"`
	MessageID    string                   `json:"message_id"`
	Type         panmailv1.EmailEventType `json:"type"`
	Recipient    string                   `json:"recipient"`
	Timestamp    time.Time                `json:"timestamp"`
	Metadata     map[string]any           `json:"metadata"`
	ErrorMessage string                   `json:"error_message"`
}

type EmailMessage struct {
	ID          string                  `json:"id"`
	TenantID    string                  `json:"tenant_id"`
	ProviderID  string                  `json:"provider_id"`
	From        string                  `json:"from"`
	To          []string                `json:"to"`
	Subject     string                  `json:"subject"`
	BodyHTML    string                  `json:"body_html"`
	BodyText    string                  `json:"body_text"`
	Attachments []*panmailv1.Attachment `json:"attachments"`
	CreatedAt   time.Time               `json:"created_at"`
}

type ArchiveInfo struct {
	ID        string    `json:"id"`
	Filename  string    `json:"filename"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

type ResourcePoint struct {
	Timestamp    time.Time `json:"timestamp"`
	CPUUsage     float64   `json:"cpu_usage"`
	MemoryUsage  uint64    `json:"memory_usage"`
	SystemLoad15 float64   `json:"system_load_15"`
}
