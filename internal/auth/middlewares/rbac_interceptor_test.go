package middlewares

import (
	"testing"
)

func TestAuthorize(t *testing.T) {
	tests := []struct {
		name      string
		procedure string
		role      string
		wantErr   bool
	}{
		{
			name:      "SignIn is public",
			procedure: "/panmail.v1.AuthService/SignIn",
			role:      "",
			wantErr:   false,
		},
		{
			name:      "SignOut is public",
			procedure: "/panmail.v1.AuthService/SignOut",
			role:      "",
			wantErr:   false,
		},
		{
			name:      "Protected method without role",
			procedure: "/panmail.v1.UserService/CreateUser",
			role:      "",
			wantErr:   true,
		},
		{
			name:      "Protected method with role",
			procedure: "/panmail.v1.UserService/CreateUser",
			role:      "USER_ROLE_ADMIN",
			wantErr:   false,
		},
		{
			name:      "Super Admin can do anything",
			procedure: "/panmail.v1.TenantService/CreateTenant",
			role:      "USER_ROLE_SUPER_ADMIN",
			wantErr:   false,
		},
		{
			name:      "Admin cannot call TenantService",
			procedure: "/panmail.v1.TenantService/CreateTenant",
			role:      "USER_ROLE_ADMIN",
			wantErr:   true,
		},
		{
			name:      "Admin cannot update tenant",
			procedure: "/panmail.v1.TenantService/UpdateTenant",
			role:      "USER_ROLE_ADMIN",
			wantErr:   true,
		},
		{
			name:      "Super Admin can update tenant",
			procedure: "/panmail.v1.TenantService/UpdateTenant",
			role:      "USER_ROLE_SUPER_ADMIN",
			wantErr:   false,
		},
		{
			name:      "Admin can update settings",
			procedure: "/panmail.v1.SystemSettingsService/UpdateSettings",
			role:      "USER_ROLE_ADMIN",
			wantErr:   false,
		},
		{
			name:      "Viewer cannot update settings",
			procedure: "/panmail.v1.SystemSettingsService/UpdateSettings",
			role:      "USER_ROLE_VIEWER",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := authorize(tt.procedure, tt.role)
			if (err != nil) != tt.wantErr {
				t.Errorf("authorize() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
