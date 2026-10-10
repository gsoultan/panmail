package usecases

import (
	"context"
	"testing"

	"github.com/gsoultan/panmail/internal/template/repositories/entities"
)

// pageRecorder records the page size the usecase asked the store for.
type pageRecorder struct{ asked int }

func (r *pageRecorder) Create(context.Context, *entities.Template) error { return nil }
func (r *pageRecorder) GetByID(context.Context, string, string) (*entities.Template, error) {
	return nil, nil
}
func (r *pageRecorder) List(_ context.Context, _ string, pageSize int, _ string) ([]*entities.Template, string, error) {
	r.asked = pageSize
	return nil, "", nil
}
func (r *pageRecorder) Update(context.Context, *entities.Template) error { return nil }
func (r *pageRecorder) Delete(context.Context, string, string) error     { return nil }

// The store took whatever page size it was given, and every template comes back
// with both bodies and its design document, so one request for a huge page
// returned a tenant's entire template library in one response.
func TestListTemplatesBoundsThePageSize(t *testing.T) {
	testCases := []struct {
		name      string
		requested int
		want      int
	}{
		{name: "unset takes the default", requested: 0, want: 50},
		{name: "negative takes the default", requested: -1, want: 50},
		{name: "a reasonable page is honoured", requested: 20, want: 20},
		{name: "the bound itself is honoured", requested: 500, want: 500},
		{name: "beyond the bound is clamped", requested: 1_000_000, want: 500},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &pageRecorder{}
			if _, _, err := NewManageTemplatesUsecase(repo).List(context.Background(), "tenant", tc.requested, ""); err != nil {
				t.Fatalf("List: %v", err)
			}
			if repo.asked != tc.want {
				t.Errorf("asked the store for %d, want %d", repo.asked, tc.want)
			}
		})
	}
}
