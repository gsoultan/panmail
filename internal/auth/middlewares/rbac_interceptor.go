package middlewares

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
)

type rbacInterceptor struct{}

func (i *rbacInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		procedure := req.Spec().Procedure

		// Extract role from context
		role := GetRole(ctx)

		// Super Admin can do anything
		if role == "USER_ROLE_SUPER_ADMIN" {
			return next(ctx, req)
		}

		// Define required roles for procedures
		if err := authorize(procedure, role); err != nil {
			// Map common auth errors to correct Connect codes
			if strings.Contains(err.Error(), "unauthenticated") {
				return nil, connect.NewError(connect.CodeUnauthenticated, err)
			}
			return nil, connect.NewError(connect.CodePermissionDenied, err)
		}

		return next(ctx, req)
	}
}

func (i *rbacInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *rbacInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		procedure := conn.Spec().Procedure
		role := GetRole(ctx)

		if role == "USER_ROLE_SUPER_ADMIN" {
			return next(ctx, conn)
		}

		if err := authorize(procedure, role); err != nil {
			if strings.Contains(err.Error(), "unauthenticated") {
				return connect.NewError(connect.CodeUnauthenticated, err)
			}
			return connect.NewError(connect.CodePermissionDenied, err)
		}

		return next(ctx, conn)
	}
}

func NewRBACInterceptor() connect.Interceptor {
	return &rbacInterceptor{}
}

func authorize(procedure string, role string) error {
	// Public procedures
	if strings.Contains(procedure, "AuthService/SignIn") ||
		strings.Contains(procedure, "AuthService/SignOut") ||
		strings.Contains(procedure, "AuthService/VerifyTwoFactor") {
		return nil
	}

	// If it's not a public procedure, role must not be empty
	if role == "" {
		return errors.New("unauthenticated")
	}

	// Procedures that require SUPER_ADMIN
	if strings.Contains(procedure, "TenantService/") {
		if role != "USER_ROLE_SUPER_ADMIN" {
			return errors.New("insufficient permissions: requires Super Admin role")
		}
	}

	// Procedures that require at least ADMINISTRATOR
	if strings.Contains(procedure, "ApiKeyService/") ||
		strings.Contains(procedure, "UserService/") ||
		strings.Contains(procedure, "AuthService/RegisterUser") ||
		strings.Contains(procedure, "SetupService/") ||
		strings.Contains(procedure, "SystemSettingsService/UpdateSettings") {
		if role != "USER_ROLE_ADMIN" && role != "USER_ROLE_SUPER_ADMIN" {
			return errors.New("insufficient permissions: requires Administrator role")
		}
	}

	// Procedures that require at least EDITOR
	if strings.Contains(procedure, "EmailProviderService/Create") ||
		strings.Contains(procedure, "EmailProviderService/Update") ||
		strings.Contains(procedure, "EmailProviderService/Delete") ||
		strings.Contains(procedure, "EmailProviderService/Test") ||
		strings.Contains(procedure, "TemplateService/Create") ||
		strings.Contains(procedure, "TemplateService/Update") ||
		strings.Contains(procedure, "TemplateService/Delete") ||
		strings.Contains(procedure, "SuppressionService/Add") ||
		strings.Contains(procedure, "SuppressionService/Remove") ||
		strings.Contains(procedure, "WebhookService/Create") ||
		strings.Contains(procedure, "WebhookService/Update") ||
		strings.Contains(procedure, "WebhookService/Delete") ||
		strings.Contains(procedure, "EmailService/SendEmail") {
		if role == "USER_ROLE_VIEWER" {
			return errors.New("insufficient permissions: requires Editor role")
		}
	}

	// VIEWERS can call anything else (List, Get, etc.)

	return nil
}
