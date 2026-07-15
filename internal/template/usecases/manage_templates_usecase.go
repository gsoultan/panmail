package usecases

import (
	"context"
	"time"

	"github.com/google/uuid"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/template/repositories/entities"
	"github.com/gsoultan/panmail/internal/template/repositories/stores"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type manageTemplatesUsecase struct {
	repo stores.TemplateRepository
}

func NewManageTemplatesUsecase(repo stores.TemplateRepository) ManageTemplatesUsecase {
	return &manageTemplatesUsecase{
		repo: repo,
	}
}

func (u *manageTemplatesUsecase) Create(ctx context.Context, tenantID string, req *panmailv1.CreateTemplateRequest) (*panmailv1.Template, error) {
	t := &entities.Template{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		Name:      req.Name,
		Subject:   req.Subject,
		BodyHTML:  req.BodyHtml,
		BodyText:  req.BodyText,
		Design:    req.Design,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := u.repo.Create(ctx, t); err != nil {
		return nil, err
	}

	return u.toProto(t), nil
}

func (u *manageTemplatesUsecase) Get(ctx context.Context, tenantID, id string) (*panmailv1.Template, error) {
	t, err := u.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, nil
	}
	return u.toProto(t), nil
}

func (u *manageTemplatesUsecase) List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*panmailv1.Template, string, error) {
	templates, nextPageToken, err := u.repo.List(ctx, tenantID, pageSize, pageToken)
	if err != nil {
		return nil, "", err
	}

	res := make([]*panmailv1.Template, len(templates))
	for i, t := range templates {
		res[i] = u.toProto(t)
	}
	return res, nextPageToken, nil
}

func (u *manageTemplatesUsecase) Update(ctx context.Context, tenantID string, req *panmailv1.UpdateTemplateRequest) (*panmailv1.Template, error) {
	t, err := u.repo.GetByID(ctx, tenantID, req.Id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, nil
	}

	t.Name = req.Name
	t.Subject = req.Subject
	t.BodyHTML = req.BodyHtml
	t.BodyText = req.BodyText
	t.Design = req.Design
	t.UpdatedAt = time.Now()

	if err := u.repo.Update(ctx, t); err != nil {
		return nil, err
	}

	return u.toProto(t), nil
}

func (u *manageTemplatesUsecase) Delete(ctx context.Context, tenantID, id string) error {
	return u.repo.Delete(ctx, tenantID, id)
}

func (u *manageTemplatesUsecase) toProto(t *entities.Template) *panmailv1.Template {
	return &panmailv1.Template{
		Id:         t.ID,
		TenantId:   t.TenantID,
		Name:       t.Name,
		Subject:    t.Subject,
		BodyHtml:   t.BodyHTML,
		BodyText:   t.BodyText,
		Design:     t.Design,
		CreateTime: timestamppb.New(t.CreatedAt),
		UpdateTime: timestamppb.New(t.UpdatedAt),
	}
}
