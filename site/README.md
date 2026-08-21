# Panmail marketing site

A standalone, zero-build marketing site for Panmail. Plain HTML, CSS and JavaScript —
no framework, no bundler, no `node_modules`. Open `index.html` and it works.

It is built to be served from the **root of a domain** (a user/org GitHub Pages repo such
as `gsoultan.github.io`), so internal links are root-relative.

```
index.html          the whole page
404.html            styled not-found page (GitHub Pages serves this automatically)
assets/css/         one stylesheet
assets/js/          one script — CTA config lives at the top
assets/img/         logo, Open Graph image (SVG source + rendered PNG)
favicon.svg
robots.txt
sitemap.xml
.nojekyll           stops Pages running Jekyll over the files
```

## Deploying to `gsoultan.github.io`

The repo root *is* the site, so copy the contents of this directory to the root of that repo:

```bash
# from the panmail repo
cp -R site/. ../gsoultan.github.io/

cd ../gsoultan.github.io
git add -A
git commit -m "Publish Panmail marketing site"
git push
```

Then in that repo: **Settings → Pages → Source: Deploy from a branch → `main` / `(root)`**.

`.nojekyll` matters — without it Pages runs the files through Jekyll, which ignores
directories beginning with an underscore and can mangle otherwise-valid output.

### Custom domain

Add a `CNAME` file at the root containing just the domain (e.g. `panmail.io`), point a
`CNAME` DNS record at `gsoultan.github.io`, and enable **Enforce HTTPS**. Then update the
absolute URLs, which only appear in four places:

- `<link rel="canonical">` and the `og:url` / `og:image` / `twitter:image` tags in `index.html`
- the `url` field in the JSON-LD block in `index.html`
- `sitemap.xml`
- `robots.txt`

## Wiring the real URLs

**Every call to action currently points at the in-page signup form.** There is no hosted
Panmail and no billing, so nothing here links to a product that exists yet. All of it is
configured in one place — the `CONFIG` block at the top of `assets/js/main.js`:

```js
const CONFIG = {
  SIGNUP_ENDPOINT: null,          // POST endpoint accepting { email }
  CTA: {
    trial:    null,               // → 'https://app.panmail.io/signup'
    signin:   null,               // → 'https://app.panmail.io/login'
    sales:    null,               // → 'https://cal.com/panmail/demo'
    selfhost: 'https://github.com/gsoultan/panmail#-getting-started',
  },
};
```

Set a value and every button carrying that `data-cta` attribute repoints itself, opening
in a new tab when the URL is off-site. Leave it `null` and the button keeps scrolling to
the signup form.

While `SIGNUP_ENDPOINT` is `null` the form validates the address and shows a confirmation
**without sending anything anywhere** — it deliberately does not POST into the void, and
does not fake success against a real endpoint. Set it to a Formspree / Buttondown /
ConvertKit / Worker URL and it starts posting `{ email }` as JSON, surfacing a visible
error if the request fails.

## Content that must stay true

The claims on this page are drawn from the product, not invented. Three are worth
re-checking before each publish, because they are the ones most likely to drift:

| Claim | Source |
| --- | --- |
| "over a thousand messages per second" | README performance section — the FAQ deliberately qualifies it, since the real ceiling is the provider's rate limit |
| PostgreSQL and SQLite only | MySQL/MariaDB connect but fail every query; the FAQ says so plainly |
| AES-256-GCM at rest, API keys as SHA-256 hashes | README security section |

**The pricing tiers, volume limits, SLA and support response times are placeholders.**
They describe an offering that does not exist yet. Replace them before this page is
public, or the page promises terms nobody has agreed to honour.

## Regenerating the Open Graph image

`assets/img/og.png` is rendered from `assets/img/og.svg`. Social platforms are unreliable
with SVG, so the PNG is what the meta tags point at. To re-render after editing the SVG:

```bash
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  --headless --disable-gpu --window-size=1200,630 \
  --screenshot=assets/img/og.png "file://$PWD/assets/img/og.svg"
```

## Local preview

```bash
python3 -m http.server 8099    # then open http://localhost:8099
```

Opening `index.html` directly over `file://` also works, but `404.html` and the
root-relative links will not resolve.

## Design notes

- Brand colours come from the product's own mark: violet `#7e14ff` → cyan `#47bfff`.
- Dark is the default. The theme follows `prefers-color-scheme` on first visit and
  remembers an explicit choice in `localStorage`.
- Everything animated is gated behind `prefers-reduced-motion`, and the scroll reveals
  fall back to plain visible content when `IntersectionObserver` is unavailable — the
  page must never be blank because a script did not run.
