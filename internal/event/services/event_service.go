package services

import (
	"context"
	"time"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/event/usecases"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type eventService struct {
	processEventUsecase usecases.ProcessEventUsecase
}

func NewEventService(processEventUsecase usecases.ProcessEventUsecase) panmailv1connect.EventServiceHandler {
	return &eventService{
		processEventUsecase: processEventUsecase,
	}
}

func (s *eventService) ListEvents(ctx context.Context, req *connect.Request[panmailv1.ListEventsRequest]) (*connect.Response[panmailv1.ListEventsResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	pageSize := int(req.Msg.PageSize)
	if pageSize == 0 {
		pageSize = 50
	}

	filter := usecases.ListFilter{
		PageSize:       pageSize,
		PageToken:      req.Msg.PageToken,
		Recipient:      req.Msg.Recipient,
		EventType:      req.Msg.EventType,
		MessageID:      req.Msg.MessageId,
		LatestOnly:     req.Msg.LatestOnly,
		RecipientExact: req.Msg.RecipientExact,
		Subject:        req.Msg.Subject,
	}

	if req.Msg.StartTime != nil {
		filter.StartTime = req.Msg.StartTime.AsTime()
	}
	if req.Msg.EndTime != nil {
		filter.EndTime = req.Msg.EndTime.AsTime()
	}

	events, nextToken, err := s.processEventUsecase.ListEvents(ctx, tenantID, filter)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&panmailv1.ListEventsResponse{
		Events:        events,
		NextPageToken: nextToken,
	}), nil
}

func (s *eventService) GetMetrics(ctx context.Context, req *connect.Request[panmailv1.GetMetricsRequest]) (*connect.Response[panmailv1.GetMetricsResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	var startTime, endTime time.Time
	if req.Msg.StartTime != nil {
		startTime = req.Msg.StartTime.AsTime()
	}
	if req.Msg.EndTime != nil {
		endTime = req.Msg.EndTime.AsTime()
	}

	metrics, extended, err := s.processEventUsecase.GetMetrics(ctx, tenantID, startTime, endTime)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&panmailv1.GetMetricsResponse{
		Metrics:         metrics,
		ExtendedMetrics: extended,
	}), nil
}

func (s *eventService) GetTimeSeriesMetrics(ctx context.Context, req *connect.Request[panmailv1.GetTimeSeriesMetricsRequest]) (*connect.Response[panmailv1.GetTimeSeriesMetricsResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	var startTime, endTime time.Time
	if req.Msg.StartTime != nil {
		startTime = req.Msg.StartTime.AsTime()
	}
	if req.Msg.EndTime != nil {
		endTime = req.Msg.EndTime.AsTime()
	}

	data, err := s.processEventUsecase.GetTimeSeriesMetrics(ctx, tenantID, startTime, endTime, req.Msg.Granularity)
	if err != nil {
		return nil, err
	}

	res := make(map[string]*panmailv1.TimeSeriesData)
	for date, metrics := range data {
		res[date] = &panmailv1.TimeSeriesData{
			Metrics: metrics,
		}
	}

	return connect.NewResponse(&panmailv1.GetTimeSeriesMetricsResponse{
		Data: res,
	}), nil
}

func (s *eventService) GetEvent(ctx context.Context, req *connect.Request[panmailv1.GetEventRequest]) (*connect.Response[panmailv1.GetEventResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	event, message, err := s.processEventUsecase.GetEvent(ctx, tenantID, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&panmailv1.GetEventResponse{
		Event:   event,
		Message: message,
	}), nil
}

func (s *eventService) GetPerformanceMetrics(ctx context.Context, req *connect.Request[panmailv1.GetPerformanceMetricsRequest]) (*connect.Response[panmailv1.GetPerformanceMetricsResponse], error) {
	metrics, err := s.processEventUsecase.GetPerformanceMetrics(ctx)
	if err != nil {
		return nil, err
	}

	history := make([]*panmailv1.ResourcePoint, len(metrics.ResourceHistory))
	for i, p := range metrics.ResourceHistory {
		history[i] = &panmailv1.ResourcePoint{
			Timestamp:     timestamppb.New(p.Timestamp),
			CpuUsage:      p.CPUUsage,
			MemoryUsage:   p.MemoryUsage,
			SystemLoad_15: p.SystemLoad15,
		}
	}

	return connect.NewResponse(&panmailv1.GetPerformanceMetricsResponse{
		SentPerSecond:   metrics.SentPerSecond,
		CpuUsage:        metrics.CPUUsage,
		MemoryUsage:     metrics.MemoryUsage,
		UptimeSeconds:   metrics.UptimeSeconds,
		Goroutines:      metrics.Goroutines,
		DiskUsage:       metrics.DiskUsage,
		OpenFiles:       metrics.OpenFiles,
		CpuCores:        metrics.CPUCores,
		TotalMemory:     metrics.TotalMemory,
		SystemLoad_15:   metrics.SystemLoad15,
		ResourceHistory: history,
	}), nil
}

func (s *eventService) ListArchives(ctx context.Context, req *connect.Request[panmailv1.ListArchivesRequest]) (*connect.Response[panmailv1.ListArchivesResponse], error) {
	pageSize := int(req.Msg.PageSize)
	if pageSize == 0 {
		pageSize = 50
	}

	archives, nextToken, err := s.processEventUsecase.ListArchives(ctx, pageSize, req.Msg.PageToken)
	if err != nil {
		return nil, err
	}

	res := make([]*panmailv1.ArchiveInfo, len(archives))
	for i, a := range archives {
		res[i] = &panmailv1.ArchiveInfo{
			Id:        a.ID,
			Filename:  a.Filename,
			Size:      a.Size,
			CreatedAt: timestamppb.New(a.CreatedAt),
		}
	}

	return connect.NewResponse(&panmailv1.ListArchivesResponse{
		Archives:      res,
		NextPageToken: nextToken,
	}), nil
}

func (s *eventService) DownloadArchive(ctx context.Context, req *connect.Request[panmailv1.DownloadArchiveRequest]) (*connect.Response[panmailv1.DownloadArchiveResponse], error) {
	content, filename, err := s.processEventUsecase.GetArchive(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&panmailv1.DownloadArchiveResponse{
		Content:  content,
		Filename: filename,
	}), nil
}
