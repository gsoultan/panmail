import { createConnectTransport } from "@connectrpc/connect-web";
import { createPromiseClient, Interceptor, ConnectError, Code } from "@connectrpc/connect";
import { AuthService, ApiKeyService, UserService, TenantService } from "../api/panmail/v1/auth_connect";
import { SetupService } from "../api/panmail/v1/setup_connect";
import { EmailProviderService } from "../api/panmail/v1/email_provider_service_connect";
import { EmailService } from "../api/panmail/v1/email_service_connect";
import { EventService } from "../api/panmail/v1/event_service_connect";
import { LogService } from "../api/panmail/v1/log_connect";
import { InboundService } from "../api/panmail/v1/inbound_service_connect";
import { WebhookService } from "../api/panmail/v1/webhook_service_connect";
import { TemplateService } from "../api/panmail/v1/template_service_connect";
import { SuppressionService } from "../api/panmail/v1/suppression_service_connect";
import { SystemSettingsService } from "../api/panmail/v1/system_settings_connect";
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

export const authClient = createPromiseClient(AuthService, transport);
export const setupClient = createPromiseClient(SetupService, transport);
export const providerClient = createPromiseClient(EmailProviderService, transport);
export const emailClient = createPromiseClient(EmailService, transport);
export const eventClient = createPromiseClient(EventService, transport);
export const logClient = createPromiseClient(LogService, transport);
export const apiKeyClient = createPromiseClient(ApiKeyService, transport);
export const userClient = createPromiseClient(UserService, transport);
export const tenantClient = createPromiseClient(TenantService, transport);
export const inboundClient = createPromiseClient(InboundService, transport);
export const webhookClient = createPromiseClient(WebhookService, transport);
export const templateClient = createPromiseClient(TemplateService, transport);
export const suppressionClient = createPromiseClient(SuppressionService, transport);
export const settingsClient = createPromiseClient(SystemSettingsService, transport);
