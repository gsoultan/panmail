import { createConnectTransport } from "@connectrpc/connect-web";
import { createClient, Interceptor, ConnectError, Code } from "@connectrpc/connect";
import { AuthService, ApiKeyService, UserService, TenantService } from "../api/panmail/v1/auth_pb";
import { SetupService } from "../api/panmail/v1/setup_pb";
import { EmailProviderService } from "../api/panmail/v1/email_provider_service_pb";
import { EmailService } from "../api/panmail/v1/email_service_pb";
import { EventService } from "../api/panmail/v1/event_service_pb";
import { LogService } from "../api/panmail/v1/log_pb";
import { InboundService } from "../api/panmail/v1/inbound_service_pb";
import { WebhookService } from "../api/panmail/v1/webhook_service_pb";
import { TemplateService } from "../api/panmail/v1/template_service_pb";
import { SuppressionService } from "../api/panmail/v1/suppression_service_pb";
import { SystemSettingsService } from "../api/panmail/v1/system_settings_pb";
import { EmailFilterService } from "../api/panmail/v1/email_filter_service_pb";
import { useAuthStore } from "../store/authStore";

const errorInterceptor: Interceptor = (next) => async (req) => {
  try {
    return await next(req);
  } catch (err) {
    if (err instanceof ConnectError && err.code === Code.Unauthenticated) {
      useAuthStore.getState().clearAuth();
      // Redirect to login if not already there
      if (window.location.pathname !== "/signin") {
        window.location.href = "/signin";
      }
    }
    throw err;
  }
};

const authInterceptor: Interceptor = (next) => async (req) => {
  const { token, selectedTenantID } = useAuthStore.getState();
  if (token) {
    req.header.set("Authorization", `Bearer ${token}`);
  }
  if (selectedTenantID) {
    req.header.set("X-Tenant-ID", selectedTenantID);
  }
  return await next(req);
};

const transport = createConnectTransport({
  baseUrl: "", // Same host as UI in production
  interceptors: [errorInterceptor, authInterceptor],
});

export const authClient = createClient(AuthService, transport);
export const setupClient = createClient(SetupService, transport);
export const providerClient = createClient(EmailProviderService, transport);
export const emailClient = createClient(EmailService, transport);
export const eventClient = createClient(EventService, transport);
export const logClient = createClient(LogService, transport);
export const apiKeyClient = createClient(ApiKeyService, transport);
export const userClient = createClient(UserService, transport);
export const tenantClient = createClient(TenantService, transport);
export const inboundClient = createClient(InboundService, transport);
export const webhookClient = createClient(WebhookService, transport);
export const templateClient = createClient(TemplateService, transport);
export const suppressionClient = createClient(SuppressionService, transport);
export const emailFilterClient = createClient(EmailFilterService, transport);
export const settingsClient = createClient(SystemSettingsService, transport);
