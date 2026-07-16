package stores

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

type EventRepository interface {
	Write(ctx context.Context, event *entities.EmailEvent) error
	List(ctx context.Context, tenantID string, filter ListFilter) ([]*entities.EmailEvent, string, error)
	GetByID(ctx context.Context, tenantID string, id string) (*entities.EmailEvent, error)
	ListByMessageID(ctx context.Context, tenantID string, messageID string) ([]*entities.EmailEvent, error)
	GetMetrics(ctx context.Context, tenantID string, startTime, endTime time.Time) (map[string]int64, error)
	GetTimeSeriesMetrics(ctx context.Context, tenantID string, startTime, endTime time.Time, granularity string) (map[string]map[string]int64, error)

	WriteMessage(ctx context.Context, message *entities.EmailMessage) error
	GetMessage(ctx context.Context, tenantID string, messageID string) (*entities.EmailMessage, error)

	TruncateBefore(ctx context.Context, before time.Time) error

	ListArchives(ctx context.Context, pageSize int, pageToken string) ([]entities.ArchiveInfo, string, error)
	GetArchive(ctx context.Context, id string) ([]byte, string, error)

	// Resource metrics for system health
	WriteResourceMetric(ctx context.Context, cpuUsage float64, memUsage uint64, load15 float64) error
	GetResourceHistory(ctx context.Context, since time.Time) ([]entities.ResourcePoint, error)

	Close() error
}
