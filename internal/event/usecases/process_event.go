package usecases

import (
	"context"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
)

type ListFilter struct {
	PageSize   int
	PageToken  string
	Recipient  string
	EventType  panmailv1.EmailEventType
	StartTime  time.Time
	EndTime    time.Time
	MessageID  string
	LatestOnly bool
}

type ResourcePoint struct {
	Timestamp    time.Time
	CPUUsage     float64
	MemoryUsage  uint64
	SystemLoad15 float64
}

type PerformanceMetrics struct {
	SentPerSecond   float64
	CPUUsage        float64
	MemoryUsage     uint64
	UptimeSeconds   uint64
	Goroutines      uint32
	DiskUsage       uint64
	OpenFiles       uint32
	CPUCores        uint32
	TotalMemory     uint64
	SystemLoad15    float64
	ResourceHistory []ResourcePoint
}

type ProcessEventUsecase interface {
	RecordEvent(ctx context.Context, tenantID, providerID, messageID string, eventType panmailv1.EmailEventType, recipient string, errorMessage string, metadata map[string]any) error
	ListEvents(ctx context.Context, tenantID string, filter ListFilter) ([]*panmailv1.EmailEvent, string, error)
	GetEvent(ctx context.Context, tenantID string, id string) (*panmailv1.EmailEvent, *panmailv1.EmailMessage, error)
	ListByMessageID(ctx context.Context, tenantID string, messageID string) ([]*panmailv1.EmailEvent, error)
	GetMetrics(ctx context.Context, tenantID string, startTime, endTime time.Time) (map[string]int64, []*panmailv1.MetricInfo, error)
	GetTimeSeriesMetrics(ctx context.Context, tenantID string, startTime, endTime time.Time, granularity string) (map[string]map[string]int64, error)

	SaveMessage(ctx context.Context, message *panmailv1.EmailMessage) error

	StartCleanupTask(ctx context.Context, interval time.Duration, retentionDays int)

	GetPerformanceMetrics(ctx context.Context) (PerformanceMetrics, error)

	ListArchives(ctx context.Context, pageSize int, pageToken string) ([]entities.ArchiveInfo, string, error)
	GetArchive(ctx context.Context, id string) ([]byte, string, error)
}

type WebhookTrigger interface {
	Enqueue(tenantID string, event panmailv1.WebhookTriggerEvent, payload any)
}
