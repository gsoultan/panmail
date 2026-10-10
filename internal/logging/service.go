package logging

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) ListLogs(
	ctx context.Context,
	req *connect.Request[panmailv1.ListLogsRequest],
) (*connect.Response[panmailv1.ListLogsResponse], error) {
	pageSize := int(req.Msg.PageSize)
	if pageSize == 0 {
		pageSize = 50
	}

	tenantID, err := logScope(ctx)
	if err != nil {
		return nil, err
	}

	entries, nextToken, err := s.store.List(pageSize, req.Msg.PageToken, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var logs []*panmailv1.LogEntry
	for _, e := range entries {
		logs = append(logs, &panmailv1.LogEntry{
			Id:        e.ID,
			Timestamp: timestamppb.New(e.Timestamp),
			Level:     e.Level,
			Message:   e.Message,
			Service:   e.Service,
			Metadata:  e.Metadata,
		})
	}

	return connect.NewResponse(&panmailv1.ListLogsResponse{
		Logs:          logs,
		NextPageToken: nextToken,
	}), nil
}

func (s *Service) StreamLogs(
	ctx context.Context,
	req *connect.Request[panmailv1.StreamLogsRequest],
	stream *connect.ServerStream[panmailv1.LogEntry],
) error {
	tenantID, err := logScope(ctx)
	if err != nil {
		return err
	}

	logCh := s.store.Subscribe(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case entry, ok := <-logCh:
			if !ok {
				return nil
			}

			if tenantID != "" && entry.TenantID != tenantID {
				continue
			}

			if err := stream.Send(&panmailv1.LogEntry{
				Id:        entry.ID,
				Timestamp: timestamppb.New(entry.Timestamp),
				Level:     entry.Level,
				Message:   entry.Message,
				Service:   entry.Service,
				Metadata:  entry.Metadata,
			}); err != nil {
				return err
			}
		}
	}
}

func (s *Service) RegisterHandler(mux *http.ServeMux) {
	mux.Handle(panmailv1connect.NewLogServiceHandler(s))
}

// logScope returns the tenant whose entries the caller may read, or "" for
// the unfiltered view, which only a super administrator gets. The lookup goes
// through the auth package's typed context key; the plain "tenant_id" string
// key this used to read never matched it, so every administrator was handed
// every tenant's log lines.
func logScope(ctx context.Context) (string, error) {
	if middlewares.GetRole(ctx) == middlewares.RoleSuperAdmin {
		return "", nil
	}
	tenantID := middlewares.GetTenantID(ctx)
	if tenantID == "" {
		return "", connect.NewError(connect.CodePermissionDenied, errors.New("no tenant in scope"))
	}
	return tenantID, nil
}
