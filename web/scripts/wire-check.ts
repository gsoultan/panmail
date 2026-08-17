/**
 * A live round trip through the regenerated client.
 *
 * The unit tests mock the transport, so nothing else proves that connect-es v2
 * and protobuf-es v2 still speak to the Go server — a version skew there
 * typechecks perfectly and fails on every request at runtime. Run against a
 * gateway:
 *
 *   PANMAIL_URL=http://localhost:8090 \
 *   PANMAIL_EMAIL=admin@panmail.local PANMAIL_PASSWORD=... \
 *   bun run wire-check
 */
import { createConnectTransport } from '@connectrpc/connect-web';
import { createClient } from '@connectrpc/connect';
import { AuthService } from '../src/api/panmail/v1/auth_pb';
import { SystemSettingsService } from '../src/api/panmail/v1/system_settings_pb';

const baseUrl = process.env.PANMAIL_URL ?? 'http://localhost:8090';
const transport = createConnectTransport({ baseUrl });

const auth = createClient(AuthService, transport);
const signIn = await auth.signIn({
  email: process.env.PANMAIL_EMAIL ?? '',
  password: process.env.PANMAIL_PASSWORD ?? '',
});
console.log('signed in as', signIn.user?.email, 'role', signIn.user?.role);

const authed = createClient(
  SystemSettingsService,
  createConnectTransport({
    baseUrl,
    interceptors: [
      (next) => async (req) => {
        req.header.set('Authorization', `Bearer ${signIn.token}`);
        return next(req);
      },
    ],
  }),
);

const { settings } = await authed.getSettings({});
console.log('retention days', {
  events: settings?.logRetentionDays,
  messages: settings?.messageRetentionDays,
  webhooks: settings?.webhookRetentionDays,
  inbound: settings?.inboundRetentionDays,
});

// A field added in this change set, read back through the new codegen: proves
// the descriptor, the wire format and the server agree.
if (settings?.webhookRetentionDays !== 7) {
  throw new Error(`expected the 7-day webhook default, got ${settings?.webhookRetentionDays}`);
}
console.log('wire check ok');
