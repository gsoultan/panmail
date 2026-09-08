import type { SmtpConnection } from '../smtpConnection';
import {
  type SnippetValues,
  PLACEHOLDER_HOST,
  envelopeAddresses,
  goString,
  goStringSlice,
  javaString,
  jsString,
  phpString,
  resolved,
} from './types';

// SMTP snippets in three languages.
//
// The one thing every generator here has to get right is that the envelope and
// the headers are different lists. A blind recipient belongs in RCPT TO and
// must not appear in the message, and the languages differ in how much help
// they give: PHPMailer and Jakarta Mail keep the two apart for you, while Go's
// net/smtp hands you both and lets you put a Bcc in a header if you build the
// message carelessly. So the Go snippet says so, at the line where it matters.

interface Endpoint {
  host: string;
  port: number;
  starttls: boolean;
}

function endpointOf(connection: SmtpConnection): Endpoint {
  return {
    host: connection.host || PLACEHOLDER_HOST,
    port: connection.port || 587,
    starttls: connection.encryption === 'STARTTLS',
  };
}

/** Go, using the standard library's net/smtp. */
export function goSmtpSnippet(values: SnippetValues, connection: SmtpConnection): string {
  const v = resolved(values);
  const { host, port } = endpointOf(connection);
  const envelope = envelopeAddresses(v.to, v.cc, v.bcc);
  const isHtml = Boolean(v.bodyHtml.trim());
  const body = isHtml ? v.bodyHtml : v.bodyText;

  const headers = [`\t\t"From: ${goString(v.from)}",`, `\t\t"To: ${goString(v.to.join(', '))}",`];
  if (v.cc.length > 0) headers.push(`\t\t"Cc: ${goString(v.cc.join(', '))}",`);
  headers.push(`\t\t"Subject: ${goString(v.subject)}",`);
  headers.push(
    `\t\t"Content-Type: ${isHtml ? 'text/html' : 'text/plain'}; charset=utf-8",`,
  );

  return `package main

import (
\t"log"
\t"net/smtp"
\t"os"
\t"strings"
)

func main() {
\thost := "${goString(host)}"
\taddr := host + ":${port}"

\t// The provider id is the username, because SMTP has nowhere else to say
\t// which provider a message goes out through. The password is an API key
\t// with the email:send scope.
\tauth := smtp.PlainAuth("", "${goString(v.providerId)}", os.Getenv("PANMAIL_API_KEY"), host)

\t// The envelope. Every address that should receive a copy goes here,
\t// blind ones included.
\trcpt := ${goStringSlice(envelope)}

\t// The headers. Only the visible lists belong here — an address that
\t// appears below stops being blind, which is the whole point of a Bcc.
\theaders := []string{
${headers.join('\n')}
\t}

\tmsg := strings.Join(headers, "\\r\\n") + "\\r\\n\\r\\n" + ${'`'}${body.replace(/`/g, '` + "`" + `')}${'`'}

\t// SendMail upgrades to STARTTLS when the server offers it, which panmail
\t// does unless it was started without a keypair.
\tif err := smtp.SendMail(addr, auth, "${goString(v.from)}", rcpt, []byte(msg)); err != nil {
\t\t// A 4xx means the message was not queued, so retrying cannot duplicate
\t\t// it. A 5xx will not succeed on a retry.
\t\tlog.Fatal(err)
\t}

\tlog.Println("submitted")
}
`;
}

/** PHP, using PHPMailer — what most existing PHP applications already have. */
export function phpSmtpSnippet(values: SnippetValues, connection: SmtpConnection): string {
  const v = resolved(values);
  const { host, port, starttls } = endpointOf(connection);
  const isHtml = Boolean(v.bodyHtml.trim());

  const recipients: string[] = [];
  for (const address of v.to) recipients.push(`$mail->addAddress('${phpString(address)}');`);
  for (const address of v.cc) recipients.push(`$mail->addCC('${phpString(address)}');`);
  // PHPMailer keeps a Bcc out of the headers itself, which is why it is safe
  // to name them here rather than hand-building the envelope.
  for (const address of v.bcc) recipients.push(`$mail->addBCC('${phpString(address)}');`);

  return `<?php

use PHPMailer\\PHPMailer\\PHPMailer;
use PHPMailer\\PHPMailer\\Exception;

require 'vendor/autoload.php';

$mail = new PHPMailer(true);

try {
    $mail->isSMTP();
    $mail->Host = '${phpString(host)}';
    $mail->Port = ${port};
    $mail->SMTPAuth = true;
${starttls ? "    $mail->SMTPSecure = PHPMailer::ENCRYPTION_STARTTLS;" : '    // This listener offers no STARTTLS, so the API key crosses the network\n    // in the clear. Only do this where the hop is already private.\n    $mail->SMTPAutoTLS = false;'}

    // The provider id is the username; the password is an API key with the
    // email:send scope.
    $mail->Username = '${phpString(v.providerId)}';
    $mail->Password = getenv('PANMAIL_API_KEY');

    $mail->setFrom('${phpString(v.from)}');
${recipients.map((line) => `    ${line}`).join('\n')}

    $mail->Subject = '${phpString(v.subject)}';
    $mail->isHTML(${isHtml ? 'true' : 'false'});
    $mail->Body = '${phpString(isHtml ? v.bodyHtml : v.bodyText)}';
${isHtml && v.bodyText ? `    $mail->AltBody = '${phpString(v.bodyText)}';\n` : ''}
    $mail->send();
    echo 'submitted', PHP_EOL;
} catch (Exception $e) {
    // A 4xx reply means the message was not queued, so a retry cannot
    // duplicate it. A 5xx will not succeed on a retry.
    throw new RuntimeException("panmail refused the send: {$mail->ErrorInfo}");
}
`;
}

/** Java, using Jakarta Mail. */
export function javaSmtpSnippet(values: SnippetValues, connection: SmtpConnection): string {
  const v = resolved(values);
  const { host, port, starttls } = endpointOf(connection);
  const isHtml = Boolean(v.bodyHtml.trim());
  const body = isHtml ? v.bodyHtml : v.bodyText;

  const recipients: string[] = [];
  for (const address of v.to) {
    recipients.push(
      `        message.addRecipient(Message.RecipientType.TO, new InternetAddress("${javaString(address)}"));`,
    );
  }
  for (const address of v.cc) {
    recipients.push(
      `        message.addRecipient(Message.RecipientType.CC, new InternetAddress("${javaString(address)}"));`,
    );
  }
  // Jakarta Mail sends a BCC recipient in the envelope without writing the
  // header, so naming them here keeps them blind.
  for (const address of v.bcc) {
    recipients.push(
      `        message.addRecipient(Message.RecipientType.BCC, new InternetAddress("${javaString(address)}"));`,
    );
  }

  return `import jakarta.mail.Authenticator;
import jakarta.mail.Message;
import jakarta.mail.PasswordAuthentication;
import jakarta.mail.Session;
import jakarta.mail.Transport;
import jakarta.mail.internet.InternetAddress;
import jakarta.mail.internet.MimeMessage;

import java.util.Properties;

public class SendEmail {
    public static void main(String[] args) throws Exception {
        Properties props = new Properties();
        props.put("mail.smtp.host", "${javaString(host)}");
        props.put("mail.smtp.port", "${port}");
        props.put("mail.smtp.auth", "true");
        // ${starttls ? 'STARTTLS is required rather than merely enabled, so a listener that stops offering it fails loudly.' : 'This listener offers no STARTTLS, so the API key crosses the network in the clear. Only do this where the hop is already private.'}
        props.put("mail.smtp.starttls.enable", "${starttls}");
${starttls ? '        props.put("mail.smtp.starttls.required", "true");\n' : ''}
        Session session = Session.getInstance(props, new Authenticator() {
            @Override
            protected PasswordAuthentication getPasswordAuthentication() {
                // The provider id is the username; the password is an API key
                // with the email:send scope.
                return new PasswordAuthentication(
                        "${javaString(v.providerId)}", System.getenv("PANMAIL_API_KEY"));
            }
        });

        MimeMessage message = new MimeMessage(session);
        message.setFrom(new InternetAddress("${javaString(v.from)}"));
${recipients.join('\n')}
        message.setSubject("${javaString(v.subject)}");
        message.setContent("${javaString(body)}", "${isHtml ? 'text/html' : 'text/plain'}; charset=utf-8");

        // A 4xx reply means the message was not queued, so a retry cannot
        // duplicate it. A 5xx will not succeed on a retry.
        Transport.send(message);
        System.out.println("submitted");
    }
}
`;
}

/** Node, using nodemailer — what most existing Node applications already have. */
export function nodeSmtpSnippet(values: SnippetValues, connection: SmtpConnection): string {
  const v = resolved(values);
  const { host, port, starttls } = endpointOf(connection);
  const isHtml = Boolean(v.bodyHtml.trim());

  const message = [
    `  from: '${jsString(v.from)}',`,
    `  to: '${jsString(v.to.join(', '))}',`,
  ];
  if (v.cc.length > 0) message.push(`  cc: '${jsString(v.cc.join(', '))}',`);
  // nodemailer puts a bcc in the envelope without writing the header, so
  // naming them here keeps them blind.
  if (v.bcc.length > 0) message.push(`  bcc: '${jsString(v.bcc.join(', '))}',`);
  message.push(`  subject: '${jsString(v.subject)}',`);
  message.push(
    isHtml
      ? `  html: '${jsString(v.bodyHtml)}',`
      : `  text: '${jsString(v.bodyText)}',`,
  );
  if (isHtml && v.bodyText) message.push(`  text: '${jsString(v.bodyText)}',`);

  return `import nodemailer from 'nodemailer';

const transporter = nodemailer.createTransport({
  host: '${jsString(host)}',
  port: ${port},
${
  starttls
    ? '  secure: false,\n  requireTLS: true,'
    : "  secure: false,\n  // This listener offers no STARTTLS, so the API key crosses the network\n  // in the clear. Only do this where the hop is already private.\n  ignoreTLS: true,"
}
  auth: {
    // The provider id is the username, because SMTP has nowhere else to say
    // which provider a message goes out through. The password is an API key
    // with the email:send scope.
    user: '${jsString(v.providerId)}',
    pass: process.env.PANMAIL_API_KEY,
  },
});

try {
  const info = await transporter.sendMail({
${message.join('\n')}
  });
  console.log('submitted', info.messageId);
} catch (error) {
  // A 4xx reply means the message was not queued, so a retry cannot duplicate
  // it. A 5xx will not succeed on a retry.
  throw error;
}
`;
}
