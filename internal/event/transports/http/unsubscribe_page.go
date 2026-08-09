package http

import (
	"fmt"
	"html"
)

// The two pages the unsubscribe endpoint serves.
//
// They are plain self-contained HTML with inline styles. This endpoint is
// reached from a mail client, often on a phone, sometimes through a proxy that
// strips external requests — so it must render correctly with no stylesheet, no
// script and no network access beyond the document itself.

const unsubscribePageStyle = `
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="robots" content="noindex, nofollow">
  <style>
    :root { color-scheme: light dark; }
    body {
      margin: 0; min-height: 100vh;
      display: flex; align-items: center; justify-content: center;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
      background: #f4f6f8; color: #1a1a1a; padding: 24px;
    }
    .card {
      background: #ffffff; border-radius: 14px; padding: 40px 32px;
      max-width: 460px; width: 100%; text-align: center;
      box-shadow: 0 10px 34px rgba(0,0,0,.09);
    }
    h1 { font-size: 21px; margin: 0 0 10px; line-height: 1.3; }
    p { font-size: 15px; line-height: 1.55; color: #555; margin: 0 0 22px; }
    .addr {
      display: inline-block; font-weight: 600; color: #1a1a1a;
      background: #f0f2f5; border-radius: 6px; padding: 3px 9px;
      word-break: break-all;
    }
    button {
      font: inherit; font-weight: 600; font-size: 15px;
      background: #d92d20; color: #fff; border: 0; border-radius: 8px;
      padding: 13px 26px; cursor: pointer; width: 100%;
      /* Comfortably above the 44px minimum touch target. */
      min-height: 48px;
    }
    button:hover { background: #b42318; }
    .done { font-size: 34px; line-height: 1; margin-bottom: 14px; }
    .muted { font-size: 13px; color: #777; margin: 18px 0 0; }
    @media (prefers-color-scheme: dark) {
      body { background: #16181d; color: #e8e8e8; }
      .card { background: #1f2229; box-shadow: none; }
      p { color: #a8adb8; }
      .addr { background: #2a2e37; color: #e8e8e8; }
      .muted { color: #8a8f99; }
    }
  </style>`

// confirmPageHTML is what a GET returns.
//
// It only offers a button that POSTs. A GET must never unsubscribe: mail
// clients, spam filters and link-scanning security software fetch every URL in
// a message, and any of them would otherwise unsubscribe the recipient without
// them ever clicking.
func confirmPageHTML(req trackingRequest) string {
	// The recipient is reflected into the page, so it is escaped. It arrives
	// base64-decoded from the path and is attacker-influenced up to the point
	// the signature is checked.
	addr := html.EscapeString(req.recipient)
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>%s<title>Unsubscribe</title></head>
<body>
  <div class="card">
    <h1>Unsubscribe from these emails?</h1>
    <p>You will stop receiving messages at<br><span class="addr">%s</span></p>
    <form method="POST">
      <button type="submit">Unsubscribe</button>
    </form>
    <p class="muted">You can be added back at any time by contacting the sender.</p>
  </div>
</body>
</html>`, unsubscribePageStyle, addr)
}

// unsubscribedPage is what a POST returns. It is static: by this point the
// address is already suppressed and there is nothing left to confirm.
const unsubscribedPage = `<!DOCTYPE html>
<html lang="en">
<head>` + unsubscribePageStyle + `<title>Unsubscribed</title></head>
<body>
  <div class="card">
    <div class="done" role="img" aria-label="Done">&#10003;</div>
    <h1>You have been unsubscribed</h1>
    <p>You will not receive any further emails of this kind.</p>
    <p class="muted">If this was a mistake, contact the sender to be added back.</p>
  </div>
</body>
</html>`
