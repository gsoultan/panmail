import type { SmtpSubmission } from '../../../api/panmail/v1/system_settings_pb';

// How the panel worked out the hostname it is showing. The panel says which,
// because "we are guessing" and "the server told us" deserve different
// confidence from whoever is about to paste it into a config file.
export type HostSource = 'listener' | 'baseUrl' | 'unknown';

export interface SmtpConnection {
  enabled: boolean;
  host: string;
  hostSource: HostSource;
  port: number;
  encryption: 'STARTTLS' | 'None';
  insecureAuthAllowed: boolean;
}

// hostFromBaseUrl pulls the hostname out of the configured public URL, which
// is the best available stand-in when the listener binds every interface.
export function hostFromBaseUrl(baseUrl: string | undefined): string {
  if (!baseUrl) return '';
  try {
    return new URL(baseUrl).hostname;
  } catch {
    return '';
  }
}

// describeConnection turns what the server reported into what the panel shows.
//
// A wildcard bind gives no hostname, so the server sends none rather than
// claiming 0.0.0.0 is reachable. Falling back to the base URL host is a guess,
// and is labelled as one.
export function describeConnection(
  submission: SmtpSubmission | undefined,
  baseUrl: string | undefined,
): SmtpConnection {
  if (!submission?.enabled) {
    return {
      enabled: false,
      host: '',
      hostSource: 'unknown',
      port: 0,
      encryption: 'None',
      insecureAuthAllowed: false,
    };
  }

  const reported = submission.host ?? '';
  const fallback = hostFromBaseUrl(baseUrl);
  const host = reported || fallback;

  let hostSource: HostSource = 'unknown';
  if (reported) hostSource = 'listener';
  else if (fallback) hostSource = 'baseUrl';

  return {
    enabled: true,
    host,
    hostSource,
    port: submission.port ?? 0,
    encryption: submission.starttls ? 'STARTTLS' : 'None',
    insecureAuthAllowed: submission.insecureAuthAllowed ?? false,
  };
}
