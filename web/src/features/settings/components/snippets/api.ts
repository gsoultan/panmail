import {
  type SnippetValues,
  goString,
  goStringSlice,
  javaList,
  javaString,
  jsArray,
  jsString,
  phpArray,
  phpString,
  resolved,
} from './types';

// The API snippets post JSON to the endpoint directly, with nothing but what
// the language ships with. They are the answer to "I do not want another
// dependency" — sdk.ts is the same sends through the published clients.
//
// Each one has to teach the thing the endpoint does not say out loud: the
// gateway answers *both* capacity refusals with 429, and only a rate limit
// carries a Retry-After. Getting that wrong turns a full queue into an
// immediate retry, which is the one response that makes it worse.

/** The procedure path. Connect derives it from the proto package and service. */
const PROCEDURE = '/panmail.v1.EmailService/SendEmail';

/** The request body, as the pretty-printed JSON every snippet embeds. */
function payloadEntries(values: SnippetValues) {
  const v = resolved(values);
  const entries: Array<[string, unknown]> = [
    ['providerId', v.providerId],
    ['from', v.from],
    ['to', v.to],
  ];
  if (v.cc.length > 0) entries.push(['cc', v.cc]);
  if (v.bcc.length > 0) entries.push(['bcc', v.bcc]);
  entries.push(['subject', v.subject]);
  if (v.bodyHtml) entries.push(['bodyHtml', v.bodyHtml]);
  if (v.bodyText) entries.push(['bodyText', v.bodyText]);
  if (values.templateId) entries.push(['templateId', values.templateId]);
  return entries;
}

/** Go, using net/http and encoding/json. */
export function goApiSnippet(values: SnippetValues, baseUrl: string): string {
  const lines = [
    'package main',
    '',
    'import (',
    '\t"bytes"',
    '\t"encoding/json"',
    '\t"log"',
    '\t"net/http"',
    '\t"os"',
    '\t"strconv"',
    ')',
    '',
    'func main() {',
    '\tpayload, err := json.Marshal(map[string]any{',
  ];

  // Padded to the longest key, because gofmt aligns a map literal's values and
  // a snippet that reformats the moment it is saved looks like it was never run.
  const entries = payloadEntries(values);
  const width = Math.max(...entries.map(([key]) => key.length));
  for (const [key, value] of entries) {
    const rendered = Array.isArray(value)
      ? goStringSlice(value as string[])
      : `"${goString(String(value))}"`;
    lines.push(`\t\t"${key}":${' '.repeat(width - key.length + 1)}${rendered},`);
  }

  lines.push(
    '\t})',
    '\tif err != nil {',
    '\t\tlog.Fatal(err)',
    '\t}',
    '',
    `\treq, err := http.NewRequest(http.MethodPost, "${goString(baseUrl)}${PROCEDURE}", bytes.NewReader(payload))`,
    '\tif err != nil {',
    '\t\tlog.Fatal(err)',
    '\t}',
    '\treq.Header.Set("Content-Type", "application/json")',
    '\t// Not Authorization: that header carries a dashboard session, and a key',
    '\t// sent as a bearer token is rejected as a malformed session rather than',
    '\t// as a bad key.',
    '\treq.Header.Set("X-API-Key", os.Getenv("PANMAIL_API_KEY"))',
    '',
    '\tres, err := http.DefaultClient.Do(req)',
    '\tif err != nil {',
    '\t\t// The one outcome where you cannot know whether the gateway took the',
    '\t\t// message. Sending is not idempotent, so do not retry blindly.',
    '\t\tlog.Fatal(err)',
    '\t}',
    '\tdefer res.Body.Close()',
    '',
    '\tif res.StatusCode == http.StatusTooManyRequests {',
    '\t\t// Both capacity refusals answer 429. Retry-After is the only thing',
    '\t\t// separating them, and neither means the message was queued.',
    '\t\tif retryAfter := res.Header.Get("Retry-After"); retryAfter != "" {',
    '\t\t\tseconds, _ := strconv.Atoi(retryAfter)',
    '\t\t\tlog.Fatalf("over the send rate, retry after %ds", seconds)',
    '\t\t}',
    '\t\tlog.Fatal("the queue is too deep; slow down rather than retry")',
    '\t}',
    '\tif res.StatusCode != http.StatusOK {',
    '\t\tlog.Fatalf("panmail refused the send: %s", res.Status)',
    '\t}',
    '',
    '\tvar result struct {',
    '\t\tMessageID string `json:"messageId"`',
    '\t\tStatus    string `json:"status"`',
    '\t}',
    '\tif err := json.NewDecoder(res.Body).Decode(&result); err != nil {',
    '\t\tlog.Fatal(err)',
    '\t}',
    '',
    '\t// Queued, not delivered: delivery is reported later, keyed by this id.',
    '\tlog.Println("queued", result.MessageID)',
    '}',
  );

  return `${lines.join('\n')}\n`;
}

/** PHP, posting JSON with the bundled cURL extension. */
export function phpApiSnippet(values: SnippetValues, baseUrl: string): string {
  const payload = payloadEntries(values).map(([key, value]) => {
    const rendered = Array.isArray(value)
      ? phpArray(value as string[])
      : `'${phpString(String(value))}'`;
    return `    '${key}' => ${rendered},`;
  });

  return `<?php

$payload = [
${payload.join('\n')}
];

$ch = curl_init('${phpString(baseUrl)}${PROCEDURE}');
curl_setopt_array($ch, [
    CURLOPT_POST => true,
    CURLOPT_RETURNTRANSFER => true,
    CURLOPT_HEADER => true,
    CURLOPT_HTTPHEADER => [
        'Content-Type: application/json',
        // Not Authorization: that header carries a dashboard session, and a
        // key sent as a bearer token is rejected as a malformed session
        // rather than as a bad key.
        'X-API-Key: ' . getenv('PANMAIL_API_KEY'),
    ],
    CURLOPT_POSTFIELDS => json_encode($payload),
]);

$response = curl_exec($ch);
if ($response === false) {
    // The one outcome where you cannot know whether the gateway took the
    // message. Sending is not idempotent, so do not retry blindly.
    throw new RuntimeException('the send did not complete: ' . curl_error($ch));
}

$status = curl_getinfo($ch, CURLINFO_HTTP_CODE);
$headerSize = curl_getinfo($ch, CURLINFO_HEADER_SIZE);
$headers = substr($response, 0, $headerSize);
$body = substr($response, $headerSize);
curl_close($ch);

if ($status === 429) {
    // Both capacity refusals answer 429. Retry-After is the only thing
    // separating them, and neither means the message was queued.
    if (preg_match('/^Retry-After:\\s*(\\d+)/mi', $headers, $m)) {
        throw new RuntimeException("over the send rate, retry after {$m[1]}s");
    }
    throw new RuntimeException('the queue is too deep; slow down rather than retry');
}
if ($status !== 200) {
    throw new RuntimeException("panmail refused the send: $status $body");
}

$result = json_decode($body, true);

// Queued, not delivered: delivery is reported later, keyed by this id.
echo "queued {$result['messageId']}", PHP_EOL;
`;
}

/** Java, posting JSON with the JDK's own HTTP client. */
export function javaApiSnippet(values: SnippetValues, baseUrl: string): string {
  const fields = payloadEntries(values).map(([key, value]) => {
    const rendered = Array.isArray(value)
      ? javaList(value as string[])
      : `"${javaString(String(value))}"`;
    return `        payload.put("${key}", ${rendered});`;
  });

  return `import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

public class SendEmail {
    public static void main(String[] args) throws Exception {
        Map<String, Object> payload = new LinkedHashMap<>();
${fields.join('\n')}

        HttpRequest request = HttpRequest.newBuilder()
                .uri(URI.create("${javaString(baseUrl)}${PROCEDURE}"))
                .header("Content-Type", "application/json")
                // Not Authorization: that header carries a dashboard session,
                // and a key sent as a bearer token is rejected as a malformed
                // session rather than as a bad key.
                .header("X-API-Key", System.getenv("PANMAIL_API_KEY"))
                .POST(HttpRequest.BodyPublishers.ofString(
                        new ObjectMapper().writeValueAsString(payload)))
                .build();

        HttpResponse<String> response = HttpClient.newHttpClient()
                .send(request, HttpResponse.BodyHandlers.ofString());

        if (response.statusCode() == 429) {
            // Both capacity refusals answer 429. Retry-After is the only thing
            // separating them, and neither means the message was queued.
            throw new IllegalStateException(response.headers()
                    .firstValue("Retry-After")
                    .map(seconds -> "over the send rate, retry after " + seconds + "s")
                    .orElse("the queue is too deep; slow down rather than retry"));
        }
        if (response.statusCode() != 200) {
            throw new IllegalStateException(
                    "panmail refused the send: " + response.statusCode() + " " + response.body());
        }

        JsonNode result = new ObjectMapper().readTree(response.body());

        // Queued, not delivered: delivery is reported later, keyed by this id.
        System.out.println("queued " + result.path("messageId").asText());
    }
}
`;
}

/** Node, posting JSON with the built-in fetch. */
export function nodeApiSnippet(values: SnippetValues, baseUrl: string): string {
  const payload = payloadEntries(values).map(([key, value]) => {
    const rendered = Array.isArray(value)
      ? jsArray(value as string[])
      : `'${jsString(String(value))}'`;
    return `    ${key}: ${rendered},`;
  });

  return `const response = await fetch('${jsString(baseUrl)}${PROCEDURE}', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    // Not Authorization: that header carries a dashboard session, and a key
    // sent as a bearer token is rejected as a malformed session rather than as
    // a bad key.
    'X-API-Key': process.env.PANMAIL_API_KEY,
  },
  body: JSON.stringify({
${payload.join('\n')}
  }),
});

if (response.status === 429) {
  // Both capacity refusals answer 429. Retry-After is the only thing
  // separating them, and neither means the message was queued.
  const retryAfter = response.headers.get('Retry-After');
  throw new Error(
    retryAfter
      ? \`over the send rate, retry after \${retryAfter}s\`
      : 'the queue is too deep; slow down rather than retry',
  );
}
if (!response.ok) {
  throw new Error(\`panmail refused the send: \${response.status} \${await response.text()}\`);
}

const result = await response.json();

// Queued, not delivered: delivery is reported later, keyed by this id.
console.log('queued', result.messageId);
`;
}
