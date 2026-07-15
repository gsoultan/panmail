package services

import (
	"context"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/template/usecases"
)

type templateService struct {
	manageTemplatesUsecase usecases.ManageTemplatesUsecase
}

func NewTemplateService(manageTemplatesUsecase usecases.ManageTemplatesUsecase) panmailv1connect.TemplateServiceHandler {
	return &templateService{
		manageTemplatesUsecase: manageTemplatesUsecase,
	}
}

func (s *templateService) CreateTemplate(ctx context.Context, req *connect.Request[panmailv1.CreateTemplateRequest]) (*connect.Response[panmailv1.CreateTemplateResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	t, err := s.manageTemplatesUsecase.Create(ctx, tenantID, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&panmailv1.CreateTemplateResponse{Template: t}), nil
}

func (s *templateService) GetTemplate(ctx context.Context, req *connect.Request[panmailv1.GetTemplateRequest]) (*connect.Response[panmailv1.GetTemplateResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	t, err := s.manageTemplatesUsecase.Get(ctx, tenantID, req.Msg.Id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&panmailv1.GetTemplateResponse{Template: t}), nil
}

func (s *templateService) ListTemplates(ctx context.Context, req *connect.Request[panmailv1.ListTemplatesRequest]) (*connect.Response[panmailv1.ListTemplatesResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	templates, nextPageToken, err := s.manageTemplatesUsecase.List(ctx, tenantID, int(req.Msg.PageSize), req.Msg.PageToken)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&panmailv1.ListTemplatesResponse{
		Templates:     templates,
		NextPageToken: nextPageToken,
	}), nil
}

func (s *templateService) UpdateTemplate(ctx context.Context, req *connect.Request[panmailv1.UpdateTemplateRequest]) (*connect.Response[panmailv1.UpdateTemplateResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	t, err := s.manageTemplatesUsecase.Update(ctx, tenantID, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&panmailv1.UpdateTemplateResponse{Template: t}), nil
}

func (s *templateService) DeleteTemplate(ctx context.Context, req *connect.Request[panmailv1.DeleteTemplateRequest]) (*connect.Response[panmailv1.DeleteTemplateResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	if err := s.manageTemplatesUsecase.Delete(ctx, tenantID, req.Msg.Id); err != nil {
		return nil, err
	}
	return connect.NewResponse(&panmailv1.DeleteTemplateResponse{}), nil
}
