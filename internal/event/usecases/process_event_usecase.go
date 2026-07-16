package usecases

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/config"
	emailstores "github.com/gsoultan/panmail/internal/email/repositories/stores"
	evententities "github.com/gsoultan/panmail/internal/event/repositories/entities"
	eventstores "github.com/gsoultan/panmail/internal/event/repositories/stores"
	inboundstores "github.com/gsoultan/panmail/internal/inbound/repositories/stores"
	"github.com/gsoultan/panmail/pkg/emailutil"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type processEventUsecase struct {
	repo           eventstores.EventRepository
	inboundRepo    inboundstores.InboundRepository
	outboxRepo     emailstores.OutboxRepository
	webhookTrigger WebhookTrigger

	sentCounter atomic.Uint64
	sentPerSec  atomic.Pointer[float64]
	startTime   time.Time
	diskUsage   atomic.Uint64
	cpuUsage    atomic.Pointer[float64]
	cpuCores    atomic.Uint32
	totalMemory atomic.Uint64
	load15      atomic.Pointer[float64]
}

func NewProcessEventUsecase(repo eventstores.EventRepository, inboundRepo inboundstores.InboundRepository, outboxRepo emailstores.OutboxRepository, webhookTrigger WebhookTrigger) ProcessEventUsecase {
	u := &processEventUsecase{
		repo:           repo,
		inboundRepo:    inboundRepo,
		outboxRepo:     outboxRepo,
		webhookTrigger: webhookTrigger,
		startTime:      time.Now(),
	}
	zero := 0.0
	u.sentPerSec.Store(&zero)
	u.cpuUsage.Store(&zero)
	u.load15.Store(&zero)

	// Initial collection for static metrics
	if cores, err := cpu.Counts(true); err == nil {
		u.cpuCores.Store(uint32(cores))
	}
	if v, err := mem.VirtualMemory(); err == nil {
		u.totalMemory.Store(v.Total)
	}

	go u.startPerformanceTracker()
	return u
}

func (u *processEventUsecase) startPerformanceTracker() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Record resource metrics every 5 minutes for history
	resourceTicker := time.NewTicker(5 * time.Minute)
	defer resourceTicker.Stop()

	for {
		select {
		case <-ticker.C:
			count := u.sentCounter.Swap(0)
			rate := float64(count) / 5.0
			u.sentPerSec.Store(&rate)

			// CPU Usage
			if percentages, err := cpu.Percent(0, false); err == nil && len(percentages) > 0 {
				u.cpuUsage.Store(&percentages[0])
			}

			// System Load
			if l, err := load.Avg(); err == nil {
				u.load15.Store(&l.Load15)
			}

			// Update disk usage periodically instead of every request
			usage := getDirSize("events.db") + getDirSize("inbound.db") + getDirSize("logs.db")
			u.diskUsage.Store(usage)

		case <-resourceTicker.C:
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			cpuVal := *u.cpuUsage.Load()
			loadVal := *u.load15.Load()
			_ = u.repo.WriteResourceMetric(context.Background(), cpuVal, m.Alloc, loadVal)
		}
	}
}

func (u *processEventUsecase) GetPerformanceMetrics(ctx context.Context) (PerformanceMetrics, error) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	diskUsage := u.diskUsage.Load()
	if diskUsage == 0 {
		// Initial load if not yet populated by tracker
		diskUsage = getDirSize("events.db") + getDirSize("inbound.db") + getDirSize("logs.db")
		u.diskUsage.Store(diskUsage)
	}

	// Try to get open files count
	var openFiles uint32
	if entries, err := os.ReadDir("/dev/fd"); err == nil {
		openFiles = uint32(len(entries))
	} else if entries, err := os.ReadDir("/proc/self/fd"); err == nil {
		openFiles = uint32(len(entries))
	}

	// Get history for the last 24 hours
	history, _ := u.repo.GetResourceHistory(ctx, time.Now().Add(-24*time.Hour))
	historyPoints := make([]ResourcePoint, len(history))
	for i, p := range history {
		historyPoints[i] = ResourcePoint{
			Timestamp:    p.Timestamp,
			CPUUsage:     p.CPUUsage,
			MemoryUsage:  p.MemoryUsage,
			SystemLoad15: p.SystemLoad15,
		}
	}

	return PerformanceMetrics{
		SentPerSecond:   *u.sentPerSec.Load(),
		CPUUsage:        *u.cpuUsage.Load(),
		MemoryUsage:     m.Alloc,
		UptimeSeconds:   uint64(time.Since(u.startTime).Seconds()),
		Goroutines:      uint32(runtime.NumGoroutine()),
		DiskUsage:       diskUsage,
		OpenFiles:       openFiles,
		CPUCores:        u.cpuCores.Load(),
		TotalMemory:     u.totalMemory.Load(),
		SystemLoad15:    *u.load15.Load(),
		ResourceHistory: historyPoints,
	}, nil
}

func getDirSize(path string) uint64 {
	var size int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return uint64(size)
}

func (u *processEventUsecase) RecordEvent(ctx context.Context, tenantID, providerID, messageID string, eventType panmailv1.EmailEventType, recipient string, errorMessage string, metadata map[string]any) error {
	// If it's a generic bounce, try to classify it better using the error message
	if eventType == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED && errorMessage != "" {
		eventType = emailutil.ClassifyError(errorMessage)
	}

	if eventType == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT {
		u.sentCounter.Add(1)
	}
	// Attempt to recover missing fields from stored message
	if (providerID == "" || recipient == "") && messageID != "" {
		msg, _ := u.repo.GetMessage(ctx, tenantID, messageID)
		if msg != nil {
			if providerID == "" {
				providerID = msg.ProviderID
			}
			if recipient == "" && len(msg.To) > 0 {
				recipient = msg.To[0]
			}
		}
	}

	e := &evententities.EmailEvent{
		ID:           uuid.New().String(),
		TenantID:     tenantID,
		ProviderID:   providerID,
		MessageID:    messageID,
		Type:         eventType,
		Recipient:    recipient,
		Timestamp:    time.Now(),
		Metadata:     metadata,
		ErrorMessage: errorMessage,
	}

	err := u.repo.Write(ctx, e)
	if err != nil {
		slog.Error("failed to write event to repository", "error", err, "id", e.ID, "type", e.Type.String())
	}

	// If it's a DELIVERED event, update the message with the provider used if not already set
	if err == nil && eventType == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED && providerID != "" && messageID != "" {
		msg, _ := u.repo.GetMessage(ctx, tenantID, messageID)
		if msg != nil && msg.ProviderID == "" {
			msg.ProviderID = providerID
			_ = u.repo.WriteMessage(ctx, msg)
		}
	}

	if err == nil && u.webhookTrigger != nil {
		triggerEvent := u.mapToWebhookTrigger(eventType)
		if triggerEvent != panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_UNSPECIFIED {
			u.webhookTrigger.Enqueue(tenantID, triggerEvent, e)
		}
	}

	return err
}

func (u *processEventUsecase) mapToWebhookTrigger(t panmailv1.EmailEventType) panmailv1.WebhookTriggerEvent {
	switch t {
	case panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT:
		return panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT
	case panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED:
		return panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_DELIVERED
	case panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED:
		return panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_OPENED
	case panmailv1.EmailEventType_EMAIL_EVENT_TYPE_CLICKED:
		return panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_CLICKED
	case panmailv1.EmailEventType_EMAIL_EVENT_TYPE_BOUNCED,
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE,
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE:
		return panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_BOUNCED
	case panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED,
		panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DROPPED:
		return panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_REJECTED
	default:
		return panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_UNSPECIFIED
	}
}

func (u *processEventUsecase) GetMetrics(ctx context.Context, tenantID string, startTime, endTime time.Time) (map[string]int64, []*panmailv1.MetricInfo, error) {
	metrics, err := u.repo.GetMetrics(ctx, tenantID, startTime, endTime)
	if err != nil {
		return nil, nil, err
	}

	// Add inbound metrics
	inboundCount, err := u.inboundRepo.Count(ctx, tenantID, startTime, endTime)
	if err == nil {
		metrics["INBOUND_RECEIVED"] = inboundCount
	}

	// Add accurate pending count from outbox
	pendingCount, err := u.outboxRepo.CountPending(ctx, tenantID)
	if err == nil {
		metrics["PENDING"] = pendingCount
	}

	// Build extended metrics with explanations
	var extended []*panmailv1.MetricInfo

	addMetric := func(key, label, desc, category string) {
		val := metrics[key]
		extended = append(extended, &panmailv1.MetricInfo{
			Key:         key,
			Label:       label,
			Description: desc,
			Value:       val,
			Category:    category,
		})
	}

	addMetric("SENT", "Emails Sent", "Total number of emails that have been successfully processed and sent to the provider.", "outbound")
	addMetric("DELIVERED", "Delivered", "Number of emails confirmed as delivered by the recipient's mail server.", "outbound")
	addMetric("OPENED", "Opened", "Number of times emails have been opened by recipients (tracked via pixel).", "outbound")
	addMetric("CLICKED", "Clicked", "Total number of links clicked within your sent emails.", "outbound")
	addMetric("BOUNCED", "Bounced", "Emails that could not be delivered due to temporary or permanent errors.", "outbound")
	addMetric("PENDING", "Pending", "Emails currently in the outbox waiting to be sent or retried.", "outbound")
	addMetric("DEFERRED", "Deferred", "Delivery attempts that were temporarily rejected and will be retried later.", "outbound")
	addMetric("INBOUND_RECEIVED", "Inbound Received", "Total number of emails received by the gateway for your domain.", "inbound")

	return metrics, extended, nil
}

func (u *processEventUsecase) GetTimeSeriesMetrics(ctx context.Context, tenantID string, startTime, endTime time.Time, granularity string) (map[string]map[string]int64, error) {
	return u.repo.GetTimeSeriesMetrics(ctx, tenantID, startTime, endTime, granularity)
}

func (u *processEventUsecase) ListByMessageID(ctx context.Context, tenantID string, messageID string) ([]*panmailv1.EmailEvent, error) {
	events, err := u.repo.ListByMessageID(ctx, tenantID, messageID)
	if err != nil {
		return nil, err
	}

	res := make([]*panmailv1.EmailEvent, len(events))
	for i, e := range events {
		meta, _ := structpb.NewStruct(e.Metadata)
		res[i] = &panmailv1.EmailEvent{
			Id:           e.ID,
			TenantId:     e.TenantID,
			ProviderId:   e.ProviderID,
			MessageId:    e.MessageID,
			Type:         e.Type,
			Recipient:    e.Recipient,
			Timestamp:    timestamppb.New(e.Timestamp),
			Metadata:     meta,
			ErrorMessage: e.ErrorMessage,
		}
	}
	return res, nil
}

func (u *processEventUsecase) ListEvents(ctx context.Context, tenantID string, filter ListFilter) ([]*panmailv1.EmailEvent, string, error) {
	repoFilter := eventstores.ListFilter{
		PageSize:  filter.PageSize,
		PageToken: filter.PageToken,
		Recipient: filter.Recipient,
		EventType: filter.EventType,
		StartTime: filter.StartTime,
		EndTime:   filter.EndTime,
		MessageID: filter.MessageID,
	}
	events, nextToken, err := u.repo.List(ctx, tenantID, repoFilter)
	if err != nil {
		return nil, "", err
	}

	res := make([]*panmailv1.EmailEvent, len(events))
	for i, e := range events {
		meta, _ := structpb.NewStruct(e.Metadata)
		res[i] = &panmailv1.EmailEvent{
			Id:           e.ID,
			TenantId:     e.TenantID,
			ProviderId:   e.ProviderID,
			MessageId:    e.MessageID,
			Type:         e.Type,
			Recipient:    e.Recipient,
			Timestamp:    timestamppb.New(e.Timestamp),
			Metadata:     meta,
			ErrorMessage: e.ErrorMessage,
		}
	}
	return res, nextToken, nil
}

func (u *processEventUsecase) GetEvent(ctx context.Context, tenantID string, id string) (*panmailv1.EmailEvent, *panmailv1.EmailMessage, error) {
	e, err := u.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, nil, err
	}
	if e == nil {
		return nil, nil, nil
	}

	meta, _ := structpb.NewStruct(e.Metadata)
	event := &panmailv1.EmailEvent{
		Id:           e.ID,
		TenantId:     e.TenantID,
		ProviderId:   e.ProviderID,
		MessageId:    e.MessageID,
		Type:         e.Type,
		Recipient:    e.Recipient,
		Timestamp:    timestamppb.New(e.Timestamp),
		Metadata:     meta,
		ErrorMessage: e.ErrorMessage,
	}

	m, err := u.repo.GetMessage(ctx, tenantID, e.MessageID)
	if err != nil {
		return event, nil, err
	}
	if m == nil {
		return event, nil, nil
	}

	message := &panmailv1.EmailMessage{
		Id:          m.ID,
		TenantId:    m.TenantID,
		ProviderId:  m.ProviderID,
		From:        m.From,
		To:          m.To,
		Subject:     m.Subject,
		BodyHtml:    m.BodyHTML,
		BodyText:    m.BodyText,
		Attachments: m.Attachments,
		CreatedAt:   timestamppb.New(m.CreatedAt),
	}

	return event, message, nil
}

func (u *processEventUsecase) SaveMessage(ctx context.Context, m *panmailv1.EmailMessage) error {
	msg := &evententities.EmailMessage{
		ID:          m.Id,
		TenantID:    m.TenantId,
		ProviderID:  m.ProviderId,
		From:        m.From,
		To:          m.To,
		Subject:     m.Subject,
		BodyHTML:    m.BodyHtml,
		BodyText:    m.BodyText,
		Attachments: m.Attachments,
		CreatedAt:   time.Now(),
	}
	return u.repo.WriteMessage(ctx, msg)
}

func (u *processEventUsecase) StartCleanupTask(ctx context.Context, interval time.Duration, defaultRetentionDays int) {
	slog.Info("starting event log cleanup task", "interval", interval, "default_retention_days", defaultRetentionDays)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run once at start
	u.runCleanup(ctx, defaultRetentionDays)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			u.runCleanup(ctx, defaultRetentionDays)
		}
	}
}

func (u *processEventUsecase) runCleanup(ctx context.Context, defaultRetentionDays int) {
	retentionDays := defaultRetentionDays
	if cfg, err := config.Load(); err == nil && cfg != nil && cfg.App.LogRetentionDays > 0 {
		retentionDays = cfg.App.LogRetentionDays
	}

	before := time.Now().AddDate(0, 0, -retentionDays)
	slog.Info("running event log cleanup", "before", before.Format(time.RFC3339), "retention_days", retentionDays)
	if err := u.repo.TruncateBefore(ctx, before); err != nil {
		slog.Error("failed to truncate event logs", "error", err)
	} else {
		slog.Info("event log cleanup successful")
	}
}

func (u *processEventUsecase) ListArchives(ctx context.Context, pageSize int, pageToken string) ([]evententities.ArchiveInfo, string, error) {
	return u.repo.ListArchives(ctx, pageSize, pageToken)
}

func (u *processEventUsecase) GetArchive(ctx context.Context, id string) ([]byte, string, error) {
	return u.repo.GetArchive(ctx, id)
}
