package middlewares

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
)

var (
	errUnauthenticated  = errors.New("authentication required")
	errUnknownProcedure = errors.New("procedure has no authorization rule")
)

type rbacInterceptor struct{}

func NewRBACInterceptor() connect.Interceptor {
	return &rbacInterceptor{}
}

func (i *rbacInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := authorize(ctx, req.Spec().Procedure); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

func (i *rbacInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *rbacInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if err := authorize(ctx, conn.Spec().Procedure); err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

// authorize decides whether the caller may invoke the procedure. Every outcome
// other than an explicit allow is a denial, including a procedure that has no
// rule at all — a new RPC is unreachable until its authority is declared.
func authorize(ctx context.Context, procedure string) error {
	rule, ok := lookupPolicy(procedure)
	if !ok {
		return connect.NewError(connect.CodePermissionDenied, errUnknownProcedure)
	}

	if rule.public {
		return nil
	}

	principal, ok := GetPrincipal(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errUnauthenticated)
	}

	switch principal.Kind {
	case PrincipalAPIKey:
		return authorizeAPIKey(principal, rule)
	case PrincipalUser:
		return authorizeUser(principal, rule)
	default:
		return connect.NewError(connect.CodeUnauthenticated, errUnauthenticated)
	}
}

func authorizeAPIKey(principal *Principal, rule access) error {
	if rule.scope == "" {
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("this endpoint cannot be called with an API key"))
	}
	if !principal.HasScope(rule.scope) {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("api key is missing the %q scope", rule.scope))
	}
	return nil
}

func authorizeUser(principal *Principal, rule access) error {
	// A super admin is above the tenant-scoped role ladder.
	if principal.Role == RoleSuperAdmin {
		return nil
	}
	if !roleSatisfies(principal.Role, rule.minRole) {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("insufficient permissions: requires %s or higher", humanRole(rule.minRole)))
	}
	return nil
}

func humanRole(role string) string {
	switch role {
	case RoleViewer:
		return "Viewer"
	case RoleEditor:
		return "Editor"
	case RoleAdmin:
		return "Administrator"
	case RoleSuperAdmin:
		return "Super Admin"
	default:
		return role
	}
}
