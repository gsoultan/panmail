package middlewares

import (
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/auth/entities"
)

// Role names as they appear in tokens and the users table.
const (
	RoleViewer     = "USER_ROLE_VIEWER"
	RoleEditor     = "USER_ROLE_EDITOR"
	RoleAdmin      = "USER_ROLE_ADMIN"
	RoleSuperAdmin = "USER_ROLE_SUPER_ADMIN"
)

var roleLevel = map[string]int{
	RoleViewer:     1,
	RoleEditor:     2,
	RoleAdmin:      3,
	RoleSuperAdmin: 4,
}

// access describes who may call one procedure.
//
// The zero value denies everyone, which is what an unlisted procedure gets.
type access struct {
	// public allows unauthenticated callers.
	public bool
	// minRole is the lowest user role accepted. Empty means no user role is
	// sufficient (i.e. the procedure is public, or API-key only).
	minRole string
	// scope is the capability an API key must hold. Empty means API keys may
	// not call this procedure at all.
	scope entities.Scope
}

// procedurePolicy is the complete authorization table. It is exhaustive by
// design: anything absent is denied, so a newly added RPC is unreachable until
// somebody states who may call it. Keys are the generated procedure constants,
// so a renamed method breaks the build rather than silently losing its rule.
var procedurePolicy = map[string]access{
	// --- Public: the sign-in handshake and first-run status ------------------
	panmailv1connect.AuthServiceSignInProcedure:          {public: true},
	panmailv1connect.AuthServiceSignOutProcedure:         {public: true},
	panmailv1connect.AuthServiceVerifyTwoFactorProcedure: {public: true},
	panmailv1connect.SetupServiceGetSetupStatusProcedure: {public: true},

	// Setup runs before any user exists, so it cannot require a role. Both of
	// these are instead guarded by the usecase, which refuses once the instance
	// is configured — otherwise TestDatabaseConnection would remain a permanent
	// unauthenticated way to dial arbitrary hosts.
	panmailv1connect.SetupServiceSetupProcedure:                  {public: true},
	panmailv1connect.SetupServiceTestDatabaseConnectionProcedure: {public: true},

	// --- Any signed-in user -------------------------------------------------
	panmailv1connect.AuthServiceGetCurrentUserProcedure:   {minRole: RoleViewer},
	panmailv1connect.AuthServiceSetupTwoFactorProcedure:   {minRole: RoleViewer},
	panmailv1connect.AuthServiceEnableTwoFactorProcedure:  {minRole: RoleViewer},
	panmailv1connect.AuthServiceDisableTwoFactorProcedure: {minRole: RoleViewer},

	// --- Super admin only ---------------------------------------------------
	panmailv1connect.TenantServiceCreateTenantProcedure: {minRole: RoleSuperAdmin},
	panmailv1connect.TenantServiceUpdateTenantProcedure: {minRole: RoleSuperAdmin},
	panmailv1connect.TenantServiceDeleteTenantProcedure: {minRole: RoleSuperAdmin},
	panmailv1connect.TenantServiceListTenantsProcedure:  {minRole: RoleSuperAdmin},

	// --- Administrator ------------------------------------------------------
	panmailv1connect.ApiKeyServiceCreateApiKeyProcedure:           {minRole: RoleAdmin},
	panmailv1connect.ApiKeyServiceListApiKeysProcedure:            {minRole: RoleAdmin},
	panmailv1connect.ApiKeyServiceDeleteApiKeyProcedure:           {minRole: RoleAdmin},
	panmailv1connect.ApiKeyServiceEnableApiKeyProcedure:           {minRole: RoleAdmin},
	panmailv1connect.ApiKeyServiceDisableApiKeyProcedure:          {minRole: RoleAdmin},
	panmailv1connect.UserServiceCreateUserProcedure:               {minRole: RoleAdmin},
	panmailv1connect.UserServiceListUsersProcedure:                {minRole: RoleAdmin},
	panmailv1connect.UserServiceDeleteUserProcedure:               {minRole: RoleAdmin},
	panmailv1connect.UserServiceUpdateUserRoleProcedure:           {minRole: RoleAdmin},
	panmailv1connect.UserServiceUpdateUserTwoFactorProcedure:      {minRole: RoleAdmin},
	panmailv1connect.SystemSettingsServiceUpdateSettingsProcedure: {minRole: RoleAdmin},

	// --- Editor: configuration changes --------------------------------------
	panmailv1connect.EmailProviderServiceCreateEmailProviderProcedure:     {minRole: RoleEditor, scope: entities.ScopeProvidersWrite},
	panmailv1connect.EmailProviderServiceUpdateEmailProviderProcedure:     {minRole: RoleEditor, scope: entities.ScopeProvidersWrite},
	panmailv1connect.EmailProviderServiceDeleteEmailProviderProcedure:     {minRole: RoleEditor, scope: entities.ScopeProvidersWrite},
	panmailv1connect.EmailProviderServiceTestEmailProviderProcedure:       {minRole: RoleEditor, scope: entities.ScopeProvidersWrite},
	panmailv1connect.EmailProviderServiceTestEmailProviderConfigProcedure: {minRole: RoleEditor, scope: entities.ScopeProvidersWrite},
	panmailv1connect.TemplateServiceCreateTemplateProcedure:               {minRole: RoleEditor, scope: entities.ScopeTemplatesWrite},
	panmailv1connect.TemplateServiceUpdateTemplateProcedure:               {minRole: RoleEditor, scope: entities.ScopeTemplatesWrite},
	panmailv1connect.TemplateServiceDeleteTemplateProcedure:               {minRole: RoleEditor, scope: entities.ScopeTemplatesWrite},
	panmailv1connect.SuppressionServiceAddSuppressionProcedure:            {minRole: RoleEditor, scope: entities.ScopeSuppressionsWrite},
	panmailv1connect.SuppressionServiceRemoveSuppressionProcedure:         {minRole: RoleEditor, scope: entities.ScopeSuppressionsWrite},
	panmailv1connect.WebhookServiceCreateWebhookProcedure:                 {minRole: RoleEditor, scope: entities.ScopeWebhooksWrite},
	panmailv1connect.WebhookServiceUpdateWebhookProcedure:                 {minRole: RoleEditor, scope: entities.ScopeWebhooksWrite},
	panmailv1connect.WebhookServiceDeleteWebhookProcedure:                 {minRole: RoleEditor, scope: entities.ScopeWebhooksWrite},

	// Sending is the one action an API key is expected to perform.
	panmailv1connect.EmailServiceSendEmailProcedure: {minRole: RoleEditor, scope: entities.ScopeEmailSend},

	// --- Viewer: read-only --------------------------------------------------
	panmailv1connect.EmailProviderServiceGetEmailProviderProcedure:   {minRole: RoleViewer, scope: entities.ScopeProvidersRead},
	panmailv1connect.EmailProviderServiceListEmailProvidersProcedure: {minRole: RoleViewer, scope: entities.ScopeProvidersRead},
	// Reads public DNS and reports on it. It touches the stored DKIM private
	// key to derive the matching public half, but returns neither the key nor
	// anything derived from it beyond a yes/no, so it stays a read.
	panmailv1connect.EmailProviderServiceCheckDomainHealthProcedure: {minRole: RoleViewer, scope: entities.ScopeProvidersRead},
	panmailv1connect.TemplateServiceGetTemplateProcedure:            {minRole: RoleViewer, scope: entities.ScopeTemplatesRead},
	panmailv1connect.TemplateServiceListTemplatesProcedure:          {minRole: RoleViewer, scope: entities.ScopeTemplatesRead},
	panmailv1connect.SuppressionServiceListSuppressionsProcedure:    {minRole: RoleViewer, scope: entities.ScopeSuppressionsRead},
	panmailv1connect.SuppressionServiceCheckSuppressionProcedure:    {minRole: RoleViewer, scope: entities.ScopeSuppressionsRead},
	panmailv1connect.WebhookServiceListWebhooksProcedure:            {minRole: RoleViewer, scope: entities.ScopeWebhooksRead},
	panmailv1connect.EventServiceListEventsProcedure:                {minRole: RoleViewer, scope: entities.ScopeEventsRead},
	panmailv1connect.EventServiceGetEventProcedure:                  {minRole: RoleViewer, scope: entities.ScopeEventsRead},
	panmailv1connect.EventServiceGetMetricsProcedure:                {minRole: RoleViewer, scope: entities.ScopeEventsRead},
	panmailv1connect.EventServiceGetTimeSeriesMetricsProcedure:      {minRole: RoleViewer, scope: entities.ScopeEventsRead},
	panmailv1connect.InboundServiceListInboundEmailsProcedure:       {minRole: RoleViewer, scope: entities.ScopeInboundRead},
	panmailv1connect.InboundServiceGetInboundEmailProcedure:         {minRole: RoleViewer, scope: entities.ScopeInboundRead},
	panmailv1connect.SystemSettingsServiceGetSettingsProcedure:      {minRole: RoleViewer},

	// Archives and host metrics describe the deployment rather than one
	// tenant's mail, so they stay with signed-in operators only.
	panmailv1connect.EventServiceListArchivesProcedure:          {minRole: RoleViewer},
	panmailv1connect.EventServiceDownloadArchiveProcedure:       {minRole: RoleViewer},
	panmailv1connect.EventServiceGetPerformanceMetricsProcedure: {minRole: RoleAdmin},
	panmailv1connect.LogServiceListLogsProcedure:                {minRole: RoleAdmin},
	panmailv1connect.LogServiceStreamLogsProcedure:              {minRole: RoleAdmin},
}

// lookupPolicy returns the rule for a procedure and whether one exists.
func lookupPolicy(procedure string) (access, bool) {
	rule, ok := procedurePolicy[procedure]
	return rule, ok
}

// roleSatisfies reports whether the caller's role meets the required minimum.
func roleSatisfies(role, minRole string) bool {
	if minRole == "" {
		return false
	}
	have, ok := roleLevel[role]
	if !ok {
		return false
	}
	return have >= roleLevel[minRole]
}
