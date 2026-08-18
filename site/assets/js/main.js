/* ============================================================
   Panmail marketing site — no dependencies.
   ============================================================ */

/* ------------------------------------------------------------------
   CONFIG — the only place you need to edit when the real product URLs
   exist. Every CTA on the page is wired through here.

   Leave a value as null and that CTA keeps its in-page anchor (#start),
   which scrolls to the signup form. Set a URL and the CTA points at it.

   SIGNUP_ENDPOINT: a POST endpoint that accepts { email }. Works with
   Formspree, Buttondown, ConvertKit, a Cloudflare Worker, etc. While it
   is null the form validates and shows a confirmation without sending
   anything anywhere — no silent data loss, no fake success on a real
   submit.
   ------------------------------------------------------------------ */
const CONFIG = {
  SIGNUP_ENDPOINT: null,                                   // e.g. 'https://formspree.io/f/xxxxxxx'
  CTA: {
    trial:    null,                                        // e.g. 'https://app.panmail.io/signup'
    signin:   null,                                        // e.g. 'https://app.panmail.io/login'
    sales:    null,                                        // e.g. 'https://cal.com/panmail/demo'
    selfhost: 'https://github.com/gsoultan/panmail#-getting-started',
  },
};

const $  = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

/* ---------- wire CTAs from CONFIG ---------- */
$$('[data-cta]').forEach((el) => {
  const url = CONFIG.CTA[el.dataset.cta];
  if (!url) return;
  el.href = url;
  if (/^https?:/i.test(url) && new URL(url, location.href).origin !== location.origin) {
    el.target = '_blank';
    el.rel = 'noopener';
  }
});

/* ---------- theme ---------- */
const root = document.documentElement;
const themeBtn = $('#theme-toggle');

const applyTheme = (theme) => {
  root.dataset.theme = theme;
  themeBtn?.setAttribute('aria-label', `Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`);
  $('meta[name="theme-color"]')?.setAttribute('content', theme === 'dark' ? '#08090f' : '#ffffff');
};

const stored = (() => {
  try { return localStorage.getItem('panmail-theme'); } catch { return null; }
})();

applyTheme(
  stored || (window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'),
);

themeBtn?.addEventListener('click', () => {
  const next = root.dataset.theme === 'dark' ? 'light' : 'dark';
  applyTheme(next);
  try { localStorage.setItem('panmail-theme', next); } catch { /* private mode */ }
});

/* ---------- sticky nav ---------- */
const nav = $('#nav');
const onScroll = () => nav?.classList.toggle('is-stuck', window.scrollY > 12);
onScroll();
window.addEventListener('scroll', onScroll, { passive: true });

/* ---------- mobile menu ---------- */
const burger = $('#burger');
const menu = $('#mobile-menu');

const setMenu = (open) => {
  if (!burger || !menu) return;
  burger.setAttribute('aria-expanded', String(open));
  burger.setAttribute('aria-label', open ? 'Close menu' : 'Open menu');
  menu.hidden = !open;
};

burger?.addEventListener('click', () => {
  setMenu(burger.getAttribute('aria-expanded') !== 'true');
});
$$('a', menu).forEach((a) => a.addEventListener('click', () => setMenu(false)));
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') setMenu(false);
});

/* ---------- reveal on scroll ---------- */
const revealables = $$('.reveal, .bars');

if (!('IntersectionObserver' in window) || reducedMotion) {
  revealables.forEach((el) => el.classList.add('is-visible'));
} else {
  const io = new IntersectionObserver(
    (entries) => {
      entries.forEach((entry) => {
        if (!entry.isIntersecting) return;
        entry.target.classList.add('is-visible');
        io.unobserve(entry.target);
      });
    },
    { rootMargin: '0px 0px -8% 0px', threshold: 0.08 },
  );

  // Stagger siblings so grids cascade instead of popping in as a block.
  const groups = new Map();
  revealables.forEach((el) => {
    const parent = el.parentElement;
    const list = groups.get(parent) || [];
    list.push(el);
    groups.set(parent, list);
  });
  groups.forEach((list) => {
    list.forEach((el, i) => {
      if (list.length > 1) el.style.setProperty('--d', `${Math.min(i, 7) * 65}ms`);
      io.observe(el);
    });
  });
}

/* ---------- animated counters ---------- */
const counters = $$('[data-count]');

const runCounter = (el) => {
  const target = Number(el.dataset.count);
  const suffix = el.dataset.suffix || '';
  if (!Number.isFinite(target)) return;

  if (reducedMotion) {
    el.textContent = target.toLocaleString() + suffix;
    return;
  }

  const duration = 1400;
  const start = performance.now();

  const tick = (now) => {
    const p = Math.min((now - start) / duration, 1);
    const eased = 1 - Math.pow(1 - p, 3);
    el.textContent = Math.round(target * eased).toLocaleString() + suffix;
    if (p < 1) requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
};

if ('IntersectionObserver' in window) {
  const co = new IntersectionObserver(
    (entries) => {
      entries.forEach((entry) => {
        if (!entry.isIntersecting) return;
        runCounter(entry.target);
        co.unobserve(entry.target);
      });
    },
    { threshold: 0.5 },
  );
  counters.forEach((el) => co.observe(el));
} else {
  counters.forEach(runCounter);
}

/* ---------- code tabs ---------- */
const tabList = $('#code-tabs');

if (tabList) {
  const tabs = $$('.tab', tabList);
  const panels = $$('.tabpanel', tabList);

  const select = (index, focus = false) => {
    tabs.forEach((tab, i) => {
      const active = i === index;
      tab.classList.toggle('is-active', active);
      tab.setAttribute('aria-selected', String(active));
      tab.tabIndex = active ? 0 : -1;
      panels[i].classList.toggle('is-active', active);
      panels[i].hidden = !active;
    });
    if (focus) tabs[index].focus();
  };

  select(0);

  tabs.forEach((tab, i) => tab.addEventListener('click', () => select(i)));

  tabList.addEventListener('keydown', (e) => {
    const current = tabs.indexOf(document.activeElement);
    if (current === -1) return;
    const moves = { ArrowRight: 1, ArrowLeft: -1, Home: -current, End: tabs.length - 1 - current };
    const delta = moves[e.key];
    if (delta === undefined) return;
    e.preventDefault();
    select((current + delta + tabs.length) % tabs.length, true);
  });
}

/* ---------- copy buttons ---------- */
$$('.copy-btn').forEach((btn) => {
  btn.addEventListener('click', async () => {
    const source = $(btn.dataset.copy);
    if (!source) return;
    try {
      await navigator.clipboard.writeText(source.innerText.trim());
      const original = btn.textContent;
      btn.textContent = 'Copied';
      btn.classList.add('is-done');
      setTimeout(() => {
        btn.textContent = original;
        btn.classList.remove('is-done');
      }, 1800);
    } catch {
      btn.textContent = 'Press ⌘C';
      setTimeout(() => { btn.textContent = 'Copy'; }, 1800);
    }
  });
});

/* ---------- pricing period toggle ---------- */
const billing = $('#billing-toggle');

billing?.addEventListener('click', (e) => {
  const opt = e.target.closest('.toggle__opt');
  if (!opt) return;

  $$('.toggle__opt', billing).forEach((b) => b.classList.toggle('is-active', b === opt));

  const yearly = opt.dataset.period === 'yearly';
  $$('.plan__amt[data-monthly]').forEach((el) => {
    el.textContent = yearly ? el.dataset.yearly : el.dataset.monthly;
  });
  $$('.plan__per').forEach((el) => {
    if (el.previousElementSibling?.dataset.monthly) {
      el.textContent = yearly ? '/ month, billed yearly' : '/ month';
    }
  });
});

/* ---------- signup form ---------- */
const form = $('#signup');

form?.addEventListener('submit', async (e) => {
  e.preventDefault();

  const input = $('#email', form);
  const button = $('button[type="submit"]', form);
  const value = input.value.trim();

  if (!value || !/^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/.test(value)) {
    input.setAttribute('aria-invalid', 'true');
    input.focus();
    return;
  }
  input.removeAttribute('aria-invalid');

  const done = (message) => {
    form.innerHTML = `<p class="signup__done">${message}</p>`;
  };

  // No endpoint wired yet: confirm locally rather than POSTing into the void.
  if (!CONFIG.SIGNUP_ENDPOINT) {
    done('Thanks — we&rsquo;ll be in touch at ' + value.replace(/[<>&]/g, '') + '.');
    return;
  }

  button.disabled = true;
  button.textContent = 'Sending…';

  try {
    const res = await fetch(CONFIG.SIGNUP_ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ email: value }),
    });
    if (!res.ok) throw new Error(String(res.status));
    done('Thanks — check your inbox to finish setting up.');
  } catch {
    button.disabled = false;
    button.textContent = 'Start free trial';
    let error = $('.signup__error', form.parentElement);
    if (!error) {
      error = document.createElement('p');
      error.className = 'cta__note signup__error';
      error.style.color = 'var(--bad)';
      error.setAttribute('role', 'alert');
      form.after(error);
    }
    error.textContent = 'That did not go through. Email hello@panmail.io and we will sort it out.';
  }
});

/* ---------- misc ---------- */
const year = $('#year');
if (year) year.textContent = String(new Date().getFullYear());
