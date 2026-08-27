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

// The SDK snippets: one call through the published client for each language,
// from panmail-sdk.
//
// The reason to prefer these over the API tab is the part nobody gets right
// from the endpoint alone — the gateway answers *both* capacity refusals with
// resource_exhausted, and only a rate limit carries a Retry-After. Every client
// here keys off that and gives you two distinct types to catch.
//
// api.ts is the same sends without a dependency, for callers who would rather
// hold the HTTP themselves.

/** Go, using github.com/gsoultan/panmail-sdk. */
export function goSdkSnippet(values: SnippetValues, baseUrl: string): string {
  const v = resolved(values);

  const lines = [
    'package main',
    '',
    'import (',
    '\t"context"',
    '\t"errors"',
    '\t"log"',
    '\t"os"',
    '',
    '\tpanmail "github.com/gsoultan/panmail-sdk"',
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
    '\t\t// Both capacity refusals mean the message was not queued. Only a',
    '\t\t// rate limit is safe to repeat, and only after its delay.',
    '\t\tvar limited *panmail.RateLimitedError',
    '\t\tif errors.As(err, &limited) {',
    '\t\t\tlog.Fatalf("over the send rate, retry after %s", limited.RetryAfter)',
    '\t\t}',
    '\t\tvar full *panmail.BacklogFullError',
    '\t\tif errors.As(err, &full) {',
    '\t\t\tlog.Fatal("the queue is too deep; slow down rather than retry")',
    '\t\t}',
    '\t\tlog.Fatal(err)',
    '\t}',
    '',
    '\t// Queued, not delivered: delivery is reported later, keyed by this id.',
    '\tlog.Println("queued", result.MessageID)',
    '}',
  );

  return `${lines.join('\n')}\n`;
}

/** PHP, using the gsoultan/panmail-sdk composer package. */
export function phpSdkSnippet(values: SnippetValues, baseUrl: string): string {
  const v = resolved(values);

  const fields = [
    `    providerId: '${phpString(v.providerId)}',`,
    `    from: '${phpString(v.from)}',`,
    `    to: ${phpArray(v.to)},`,
  ];
  fields.push(`    subject: '${phpString(v.subject)}',`);
  if (v.bodyHtml) fields.push(`    html: '${phpString(v.bodyHtml)}',`);
  if (v.bodyText) fields.push(`    text: '${phpString(v.bodyText)}',`);
  if (v.cc.length > 0) fields.push(`    cc: ${phpArray(v.cc)},`);
  if (v.bcc.length > 0) fields.push(`    bcc: ${phpArray(v.bcc)},`);
  if (values.templateId) fields.push(`    templateId: '${phpString(values.templateId)}',`);

  return `<?php

require 'vendor/autoload.php';

use Panmail\\Client;
use Panmail\\Message;
use Panmail\\Exception\\BacklogFullException;
use Panmail\\Exception\\RateLimitedException;

$client = new Client('${phpString(baseUrl)}', getenv('PANMAIL_API_KEY'));

try {
    $result = $client->send(new Message(
${fields.join('\n')}
    ));

    // Queued, not delivered: delivery is reported later, keyed by this id.
    echo "queued {$result->messageId}", PHP_EOL;
} catch (RateLimitedException $e) {
    // Not queued, and safe to repeat after the delay.
    throw new RuntimeException("over the send rate, retry after {$e->retryAfter}s");
} catch (BacklogFullException $e) {
    // Not queued either, but retrying on a timer makes the wait longer for
    // everything already queued. Slow down, or stop.
    throw new RuntimeException('the queue is too deep; slow down rather than retry');
}
`;
}

/** Java, using the io.github.gsoultan:panmail-sdk artifact. */
export function javaSdkSnippet(values: SnippetValues, baseUrl: string): string {
  const v = resolved(values);

  const builder = [
    `                .providerId("${javaString(v.providerId)}")`,
    `                .from("${javaString(v.from)}")`,
    `                .to(${javaList(v.to)})`,
  ];
  if (v.cc.length > 0) builder.push(`                .cc(${javaList(v.cc)})`);
  if (v.bcc.length > 0) builder.push(`                .bcc(${javaList(v.bcc)})`);
  builder.push(`                .subject("${javaString(v.subject)}")`);
  if (v.bodyHtml) builder.push(`                .html("${javaString(v.bodyHtml)}")`);
  if (v.bodyText) builder.push(`                .text("${javaString(v.bodyText)}")`);
  if (values.templateId) {
    builder.push(`                .templateId("${javaString(values.templateId)}")`);
  }

  return `import io.github.gsoultan.panmail.BacklogFullException;
import io.github.gsoultan.panmail.Message;
import io.github.gsoultan.panmail.PanmailClient;
import io.github.gsoultan.panmail.RateLimitedException;
import io.github.gsoultan.panmail.Result;

// to() always renders a List.of(...), so this import is never unused.
import java.util.List;

public class SendEmail {
    public static void main(String[] args) {
        PanmailClient client = PanmailClient.builder()
                .baseUrl("${javaString(baseUrl)}")
                .apiKey(System.getenv("PANMAIL_API_KEY"))
                .build();

        try {
            Result result = client.send(Message.builder()
${builder.join('\n')}
                    .build());

            // Queued, not delivered: delivery is reported later, keyed by this id.
            System.out.println("queued " + result.messageId());
        } catch (RateLimitedException e) {
            // Not queued, and safe to repeat after the delay.
            throw new IllegalStateException("over the send rate, retry after " + e.retryAfter());
        } catch (BacklogFullException e) {
            // Not queued either, but retrying on a timer makes the wait longer
            // for everything already queued. Slow down, or stop.
            throw new IllegalStateException("the queue is too deep; slow down rather than retry");
        }
    }
}
`;
}

/** Node, using the @gsoultan/panmail-sdk npm package. */
export function nodeSdkSnippet(values: SnippetValues, baseUrl: string): string {
  const v = resolved(values);

  const fields = [
    `    providerId: '${jsString(v.providerId)}',`,
    `    from: '${jsString(v.from)}',`,
    `    to: ${jsArray(v.to)},`,
  ];
  if (v.cc.length > 0) fields.push(`    cc: ${jsArray(v.cc)},`);
  if (v.bcc.length > 0) fields.push(`    bcc: ${jsArray(v.bcc)},`);
  fields.push(`    subject: '${jsString(v.subject)}',`);
  if (v.bodyHtml) fields.push(`    html: '${jsString(v.bodyHtml)}',`);
  if (v.bodyText) fields.push(`    text: '${jsString(v.bodyText)}',`);
  if (values.templateId) fields.push(`    templateId: '${jsString(values.templateId)}',`);

  return `import { PanmailClient, BacklogFullError, RateLimitedError } from '@gsoultan/panmail-sdk';

const client = new PanmailClient('${jsString(baseUrl)}', process.env.PANMAIL_API_KEY);

try {
  const result = await client.send({
${fields.join('\n')}
  });

  // Queued, not delivered: delivery is reported later, keyed by this id.
  console.log('queued', result.messageId);
} catch (error) {
  // Both capacity refusals mean the message was not queued. Only a rate limit
  // is safe to repeat, and only after its delay.
  if (error instanceof RateLimitedError) {
    throw new Error(\`over the send rate, retry after \${error.retryAfter}s\`);
  }
  if (error instanceof BacklogFullError) {
    // Retrying on a timer makes the wait longer for everything already queued.
    throw new Error('the queue is too deep; slow down rather than retry');
  }
  throw error;
}
`;
}
