package middlewares

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/auth/entities"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func userContext(role string) context.Context {
	return WithPrincipal(context.Background(), &Principal{
		Kind:     PrincipalUser,
		UserID:   "user-1",
		TenantID: "tenant-1",
		Role:     role,
	})
}

func apiKeyContext(scopes ...entities.Scope) context.Context {
	return WithPrincipal(context.Background(), &Principal{
		Kind:     PrincipalAPIKey,
		TenantID: "tenant-1",
		Scopes:   scopes,
	})
}

func TestAuthorizeUserRoles(t *testing.T) {
	tests := []struct {
		name      string
		procedure string
		ctx       context.Context
		wantErr   bool
	}{
		{"SignIn is public", panmailv1connect.AuthServiceSignInProcedure, context.Background(), false},
		{"SignOut is public", panmailv1connect.AuthServiceSignOutProcedure, context.Background(), false},
		{"VerifyTwoFactor is public", panmailv1connect.AuthServiceVerifyTwoFactorProcedure, context.Background(), false},

		{"protected method unauthenticated", panmailv1connect.UserServiceCreateUserProcedure, context.Background(), true},
		{"protected method with role", panmailv1connect.UserServiceCreateUserProcedure, userContext(RoleAdmin), false},

		{"super admin may create tenants", panmailv1connect.TenantServiceCreateTenantProcedure, userContext(RoleSuperAdmin), false},
		{"admin may not create tenants", panmailv1connect.TenantServiceCreateTenantProcedure, userContext(RoleAdmin), true},
		{"admin may not update tenants", panmailv1connect.TenantServiceUpdateTenantProcedure, userContext(RoleAdmin), true},
		{"super admin may update tenants", panmailv1connect.TenantServiceUpdateTenantProcedure, userContext(RoleSuperAdmin), false},

		{"admin may update settings", panmailv1connect.SystemSettingsServiceUpdateSettingsProcedure, userContext(RoleAdmin), false},
		{"viewer may not update settings", panmailv1connect.SystemSettingsServiceUpdateSettingsProcedure, userContext(RoleViewer), true},

		{"viewer may list templates", panmailv1connect.TemplateServiceListTemplatesProcedure, userContext(RoleViewer), false},
		{"viewer may not create templates", panmailv1connect.TemplateServiceCreateTemplateProcedure, userContext(RoleViewer), true},
		{"editor may create templates", panmailv1connect.TemplateServiceCreateTemplateProcedure, userContext(RoleEditor), false},

		{"unknown role is denied", panmailv1connect.TemplateServiceListTemplatesProcedure, userContext("USER_ROLE_WHATEVER"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := authorize(tc.ctx, tc.procedure)
			if (err != nil) != tc.wantErr {
				t.Errorf("authorize(%s) error = %v, wantErr %v", tc.procedure, err, tc.wantErr)
			}
		})
	}
}

// An RPC with no rule must be unreachable, so that adding a method to a proto
// cannot silently expose it.
func TestAuthorizeDeniesUnknownProcedures(t *testing.T) {
	for _, ctx := range []context.Context{
		context.Background(),
		userContext(RoleSuperAdmin),
		apiKeyContext(entities.ScopeEmailSend),
	} {
		if err := authorize(ctx, "/panmail.v1.FutureService/DoSomething"); err == nil {
			t.Error("expected an unlisted procedure to be denied")
		}
	}
}

// Every procedure the server actually serves needs a rule, or it is dead on
// arrival. This is the other half of deny-by-default: it catches a new RPC at
// test time rather than in production.
func TestEveryProcedureHasAPolicy(t *testing.T) {
	for _, procedure := range allServedProcedures() {
		if _, ok := lookupPolicy(procedure); !ok {
			t.Errorf("procedure %s has no authorization rule", procedure)
		}
	}
}

func TestAuthorizeAPIKeyScopes(t *testing.T) {
	tests := []struct {
		name      string
		procedure string
		scopes    []entities.Scope
		wantErr   bool
	}{
		{
			name:      "send scope may send",
			procedure: panmailv1connect.EmailServiceSendEmailProcedure,
			scopes:    []entities.Scope{entities.ScopeEmailSend},
		},
		{
			name:      "send scope may not write providers",
			procedure: panmailv1connect.EmailProviderServiceCreateEmailProviderProcedure,
			scopes:    []entities.Scope{entities.ScopeEmailSend},
			wantErr:   true,
		},
		{
			name:      "send scope may not write webhooks",
			procedure: panmailv1connect.WebhookServiceCreateWebhookProcedure,
			scopes:    []entities.Scope{entities.ScopeEmailSend},
			wantErr:   true,
		},
		{
			name:      "granted provider write scope may write providers",
			procedure: panmailv1connect.EmailProviderServiceCreateEmailProviderProcedure,
			scopes:    []entities.Scope{entities.ScopeProvidersWrite},
		},
		{
			name:      "api keys may never manage users",
			procedure: panmailv1connect.UserServiceCreateUserProcedure,
			scopes:    []entities.Scope{entities.ScopeEmailSend, entities.ScopeProvidersWrite},
			wantErr:   true,
		},
		{
			name:      "api keys may never mint more api keys",
			procedure: panmailv1connect.ApiKeyServiceCreateApiKeyProcedure,
			scopes:    []entities.Scope{entities.ScopeEmailSend},
			wantErr:   true,
		},
		{
			name:      "api keys may never manage tenants",
			procedure: panmailv1connect.TenantServiceCreateTenantProcedure,
			scopes:    []entities.Scope{entities.ScopeEmailSend},
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := authorize(apiKeyContext(tc.scopes...), tc.procedure)
			if (err != nil) != tc.wantErr {
				t.Errorf("authorize(%s) error = %v, wantErr %v", tc.procedure, err, tc.wantErr)
			}
		})
	}
}

// allServedProcedures lists every procedure the generated handlers register.
//
// Derived from the compiled descriptors rather than written out by hand. The
// hand-written list this replaces had to be extended by whoever added an RPC,
// and nothing enforced that: a new procedure was simply absent from the list,
// so TestEveryProcedureHasAPolicy passed without ever having looked at it.
// A test that silently stops covering the thing it was written for is worse
// than no test, and this one guards deny-by-default.
func allServedProcedures() []string {
	var procedures []string
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if fd.Package() != "panmail.v1" {
			return true
		}
		services := fd.Services()
		for i := range services.Len() {
			service := services.Get(i)
			methods := service.Methods()
			for j := range methods.Len() {
				procedures = append(procedures,
					fmt.Sprintf("/%s/%s", service.FullName(), methods.Get(j).Name()))
			}
		}
		return true
	})
	sort.Strings(procedures)
	return procedures
}
