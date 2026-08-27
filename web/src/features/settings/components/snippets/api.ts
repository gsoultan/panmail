import {
  type SnippetValues,
  goString,
  goStringSlice,
  javaList,
  javaString,
  phpArray,
  phpString,
  resolved,
} from './types';

// The API snippets call the same endpoint the cURL tab shows. Go gets the
// published client because it exists and handles the refusals; PHP and Java
// post JSON directly, which is all the endpoint asks for.

/** Go, using the published client from pkg/panmail. */
export function goApiSnippet(values: SnippetValues, baseUrl: string): string {
  const v = resolved(values);

  const lines = [
    'package main',
    '',
    'import (',
    '\t"context"',
    '\t"log"',
    '\t"os"',
    '',
    '\t"github.com/gsoultan/panmail/pkg/panmail"',
    ')',
    '',
    'func main() {',
    `\tclient, err := panmail.New("${goString(baseUrl)}", os.Getenv("PANMAIL_API_KEY"))`,
    '\tif err != nil {',
    '\t\tlog.Fatal(err)',
    '\t}',
    '',
    '\tresult, err := client.Send(context.Background(), panmail.Message{',
    `\t\tProviderID: "${goString(v.providerId)}",`,
    `\t\tFrom:       "${goString(v.from)}",`,
    `\t\tTo:         ${goStringSlice(v.to)},`,
  ];

  if (v.cc.length > 0) lines.push(`\t\tCc:         ${goStringSlice(v.cc)},`);
  if (v.bcc.length > 0) lines.push(`\t\tBcc:        ${goStringSlice(v.bcc)},`);

  lines.push(`\t\tSubject:    "${goString(v.subject)}",`);
  if (v.bodyHtml) lines.push(`\t\tHTML:       "${goString(v.bodyHtml)}",`);
  if (v.bodyText) lines.push(`\t\tText:       "${goString(v.bodyText)}",`);
  if (values.templateId) lines.push(`\t\tTemplateID: "${goString(values.templateId)}",`);

  lines.push(
    '\t})',
    '\tif err != nil {',
    '\t\t// RateLimitedError and BacklogFullError are the two capacity refusals;',
    '\t\t// neither means the message was queued.',
    '\t\tlog.Fatal(err)',
    '\t}',
    '',
    '\tlog.Println("queued", result.MessageID)',
    '}',
  );

  return lines.join('\n');
}

/** PHP, posting JSON with the bundled cURL extension. */
export function phpApiSnippet(values: SnippetValues, baseUrl: string): string {
  const v = resolved(values);

  const payload = [
    `    'providerId' => '${phpString(v.providerId)}',`,
    `    'from' => '${phpString(v.from)}',`,
    `    'to' => ${phpArray(v.to)},`,
  ];
  if (v.cc.length > 0) payload.push(`    'cc' => ${phpArray(v.cc)},`);
  if (v.bcc.length > 0) payload.push(`    'bcc' => ${phpArray(v.bcc)},`);
  payload.push(`    'subject' => '${phpString(v.subject)}',`);
  if (v.bodyHtml) payload.push(`    'bodyHtml' => '${phpString(v.bodyHtml)}',`);
  if (v.bodyText) payload.push(`    'bodyText' => '${phpString(v.bodyText)}',`);
  if (values.templateId) payload.push(`    'templateId' => '${phpString(values.templateId)}',`);

  return `<?php

$payload = [
${payload.join('\n')}
];

$ch = curl_init('${phpString(baseUrl)}/panmail.v1.EmailService/SendEmail');
curl_setopt_array($ch, [
    CURLOPT_POST => true,
    CURLOPT_RETURNTRANSFER => true,
    CURLOPT_HTTPHEADER => [
        'Content-Type: application/json',
        // Not Authorization: that header carries a dashboard session, and a
        // key sent as a bearer token is rejected as a malformed session.
        'X-API-Key: ' . getenv('PANMAIL_API_KEY'),
    ],
    CURLOPT_POSTFIELDS => json_encode($payload),
]);

$response = curl_exec($ch);
$status = curl_getinfo($ch, CURLINFO_HTTP_CODE);
curl_close($ch);

if ($status !== 200) {
    // 429 is the send rate; a 429 without Retry-After is a full queue.
    // Neither means the message was queued.
    throw new RuntimeException("panmail refused the send: $status $response");
}

echo $response, PHP_EOL;
`;
}

/** Java, posting JSON with the JDK's own HTTP client. */
export function javaApiSnippet(values: SnippetValues, baseUrl: string): string {
  const v = resolved(values);

  const fields = [
    `        payload.put("providerId", "${javaString(v.providerId)}");`,
    `        payload.put("from", "${javaString(v.from)}");`,
    `        payload.put("to", ${javaList(v.to)});`,
  ];
  if (v.cc.length > 0) fields.push(`        payload.put("cc", ${javaList(v.cc)});`);
  if (v.bcc.length > 0) fields.push(`        payload.put("bcc", ${javaList(v.bcc)});`);
  fields.push(`        payload.put("subject", "${javaString(v.subject)}");`);
  if (v.bodyHtml) fields.push(`        payload.put("bodyHtml", "${javaString(v.bodyHtml)}");`);
  if (v.bodyText) fields.push(`        payload.put("bodyText", "${javaString(v.bodyText)}");`);
  if (values.templateId) {
    fields.push(`        payload.put("templateId", "${javaString(values.templateId)}");`);
  }

  return `import com.fasterxml.jackson.databind.ObjectMapper;

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
                .uri(URI.create("${javaString(baseUrl)}/panmail.v1.EmailService/SendEmail"))
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

        if (response.statusCode() != 200) {
            // 429 is the send rate; a 429 without Retry-After is a full queue.
            // Neither means the message was queued.
            throw new IllegalStateException(
                    "panmail refused the send: " + response.statusCode() + " " + response.body());
        }

        System.out.println(response.body());
    }
}
`;
}
