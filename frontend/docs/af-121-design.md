# AF-121: XUI console foundations

Reference and package verification: November 12, 2026. Scope: the application shell,
shared presentation tokens, and the complete Dashboard. Anti-Fraud remains an
independent project, without Xsolla product branding or sample account/billing tools.

## References

- [Current brandbook](https://brand.xsolla.com/document/28#/-/home)
- [Colors](https://brandbook.xsolla.com/document/28#/-/colors-1)
- [Typography](https://brandbook.xsolla.com/document/28#/-/typography-1)
- [Product screens](https://brandbook.xsolla.com/document/28#/-/product-screens-1)
- [Approved screen Figma](https://www.figma.com/design/0RM2IejaQH9LnhrWpL7yQX/Marketing-Screens-Monetisation---OUT?node-id=1539-12779)
- [XUI base](https://xsolla-ui-toolkit-v2.web.app/?path=/docs/docs-getting-started--docs)
- [XUI B2B](https://xsolla-ui-toolkit-v2.web.app/b2b/?path=/docs/docs-getting-started--docs)
- [Sidebar API](https://xsolla-ui-toolkit-v2.web.app/b2b/?path=/docs/navigation-sidebar--docs)
- [XUI typography](https://xsolla-ui-toolkit-v2.web.app/?path=/docs/docs-typography--docs)
- [Identity case study](https://www.pentagram.com/work/xsolla)
- [WCAG 2.2](https://www.w3.org/WAI/WCAG22/quickref/)

The current brandbook leads B2B with light surfaces, regular-width Pilat, and restrained
Spark Blue. The Figma file supplies approved screen examples, not a reusable analytical
Dashboard specification. Light is the visual reference; system-preference dark remains
fully supported. AF-121 originally followed the system preference; the subsequent manual
appearance control is documented in the follow-up section below.

## Packages and integration

Direct XUI packages are pinned to **0.230.0**: core, b2b-sidebar, icons-base, button,
typography, and status. styled-components is pinned to **6.6.0**. The published XUI
packages declare MIT licensing. React/React DOM remain **19.3.1**, TypeScript **7.0.2**,
Vite **8.3.6**, and Recharts **3.10.1**. Browser-only development dependencies are
Playwright **1.65.0** and axe-core/Playwright **4.14.0**.

Normal npm installation and a clean Node 24 Linux install pass without overrides or
legacy peer resolution. The package tree contains no React Native packages. An optional
native icon peer causes an additional `--strict-peer-deps` experiment to fail; that flag
is not required or configured. Public peer ranges alone are not the compatibility proof:
consumer compilation, production build, and browser rendering are required checks.
Published declarations govern props where documentation examples differ. The provider
uses `initialProductContext="b2b"`; controls use `onPress` and explicit button types.

The shell composes the B2B menu primitives in a native navigation landmark. It avoids
the high-level sidebar's simultaneously mounted, opacity-hidden panes. A native dialog
contains the narrow-screen navigation. Its native close button avoids an XUI key-up
handoff where the Enter release from opening the drawer could activate the newly focused
close control. Form submission controls on other pages retain native semantics.

## Fonts

Abil Bakhtiyarli confirmed authorization for Pilat and Aktiv Grotesk for this side project
on November 12, 2026. This owner confirmation is the font-use basis; the packages' MIT
metadata and public URLs are not a font licence. Separate reference terms:
[Pilat web licensing](https://www.generaltypestudio.com/licenses/web) and
[Aktiv Grotesk licensing](https://www.daltonmaag.com/font-library/aktiv-grotesk.html).

Delivery uses the provider's default hosted font declarations at
`https://cdn.xsolla.net/xsolla-ui-toolkit-prod/fonts/v1`. No font files are copied,
self-hosted, or installed on the operating system. Pilat 400/500 and Aktiv Grotesk Latin
400/500/600 returned HTTP 200 with WOFF2 content and CORS access. Heading and body faces
are verified loaded in browser tests. The provider uses `font-display: swap` and runtime
fallback stacks. Fallback is resilience, not the accepted finished typography. Recheck
font authorization before reusing these assets outside the confirmed project scope.

## Composition and tokens

- Desktop: a 280px sidebar, a quiet readiness header, and a flexible analytical canvas.
  Below 64rem the same six destinations appear in a modal drawer. Route changes do not
  introduce a routing dependency; native hash URLs and browser history remain intact.
- Four headline outcome/count cards precede three operational cards. The queue card
  explicitly says all time. The timeline spans the canvas; rules/model panels share a
  row where space permits; quality evaluation has its own full-width section.
- Pilat headings use XUI h3/h5 visual roles with semantic h1/h2 elements. Aktiv Grotesk
  carries controls, body copy, and tabular data. Large values retain their full precision.
- `ConsoleTheme` is the only source of mode and exports XUI backgrounds, content, borders,
  outcome palettes, 4/8/16/24/32 spacing, typography roles, and shape into CSS variables.
  Document-level variables cover portals and native controls. Legacy rules are layered
  beneath XUI styles; custom focus and outcome treatments are deliberate application rules.
- Spark Blue is a selected-control background paired with black text. White/Spark has
  only 1.39:1 contrast. XUI's light cyan focus border has 1.95:1 against white, so the
  application uses primary-content outlines. Native input boundaries use tertiary content.
- Outcome labels use primary text on semantic tinted surfaces, with distinct symbols.
  Chart series are solid/diagonal/dotted with separators and a textual counts table.
  Small text never relies on the light warning/success foregrounds as its only treatment.
- Recharts, native radio/select controls, semantic tables, shared inline badges, panels,
  and loading/error components remain lightweight retained components using these tokens.

## Verification and evidence

Commands from `frontend/`:

```sh
npm ci
npm run check
npm run build
npx playwright install chromium firefox webkit
npm run test:browser
```

CI installs Chromium and runs its complete suite on Node 24. Local browser verification
also covers Firefox and WebKit. WebKit's default macOS link navigation uses Option-Tab;
this platform preference is respected rather than changing the user's system settings.

The pre-migration baseline passed 24 Node tests and build, with one pre-existing Simulator
refs warning. Node 24 clean install/check/build also pass. XUI increases the application
entry from approximately 91 to 143 kB gzip; Vite reports its 500 kB uncompressed chunk
warning. The chart remains lazy-loaded. The warning is not suppressed.

The complete local run passed **102 browser tests** across Chromium, Firefox, and WebKit,
in addition to the 24 Node tests.

All browser fixtures are hand-authored synthetic data, never IEEE-CIS-derived rows.
API interception exercises writes without touching backend records or embedding service
keys. The existing proxy-origin/security tests remain unchanged. Screenshots fix time,
locale, timezone, and viewport; populated captures wait for the chart and fonts.

The browser suite writes sanitized screenshots and computed contrast reports under
the system temporary directory's `af121-evidence/`: `before/`, `after/`, `states/`,
and `contrast/`. Set `AF_EVIDENCE_DIR` to override that directory (the original local
captures used `/private/tmp/af121-evidence`). Baseline
captures used `AF_SCREENSHOT_PHASE=before` before presentation changes. That switch skips
only the new layout assertion and must not be used for acceptance. Screenshot artifacts
are for visual review; automated pixel comparisons are not a substitute for that review.

| Criterion | Evidence |
|---|---|
| 1 | References, pinned versions, font decision in this note |
| 2 | Normal install, valid dependency tree, Node 24/26 checks/build, three-browser rendering |
| 3 | One B2B provider; light/dark accessibility scans and live preference-change assertions |
| 4 | Direct hashes, active route, Back/Forward, fallback, drawer keyboard/focus/resize checks |
| 5 | Theme bridge, layered retained styles, typography/spacing/outcome/focus mapping |
| 6 | Seven metric values/units, all-time queue note, period activation, UTC table, source/run isolation |
| 7 | Loading/empty/error/stale captures, independent failure/retry tests, absent denominator semantics |
| 8 | Six-route width checks and synthetic review, Blocklist, Rulesets, Tester, Simulator workflows |
| 9 | axe scans plus 14 actual text/boundary/focus contrast samples per theme/browser; keyboard tests |
| 10 | 390/768/1280 plus 195/384/640 CSS-pixel reflow checks on all routes in both themes |
| 11 | Loaded font faces and blocked-CDN fallback checks with a large minor-unit value |
| 12 | Node checks/build, focused browser suite, sanitized before/after and state screenshots |

Curated evidence retained with this note:
[before](af-121/before-light-desktop.png), [light desktop](af-121/after-light-desktop.png),
[dark desktop](af-121/after-dark-desktop.png), [mobile](af-121/after-light-mobile.png),
[loading](af-121/loading-mobile.png), [empty](af-121/empty-mobile.png),
[error](af-121/error-mobile.png), [stale](af-121/stale-mobile.png),
[light contrast](af-121/contrast-light.json), [dark contrast](af-121/contrast-dark.json).

Contrast reports composite actual computed foreground/background colors, including alpha.
They measure the rendered XUI label rather than the independent button wrapper color.
Sampled ratios are at least 4.76:1 in light and 4.95:1 in dark. Normal text requires 4.5:1;
control boundaries and custom focus require 3:1. axe does not establish full WCAG conformance.

Manual Safari verification confirmed **200% actual page zoom** through Safari's page
menu, inspected the enlarged Dashboard, opened the drawer, and navigated to Review queue.
Zoom was restored and the temporary tab closed. Automated half-width checks exercise
reflow at the three target widths; they are not claimed to emulate browser zoom itself.

## Preserved behavior and follow-ups

Statistics still poll every five seconds, readiness every ten, and quality independently
of Dashboard period. Cancellation, period guards, last-good results, retry, source/run keys,
pagination, UTC buckets, microsecond conversion, exact money formatting, and quality
semantics are unchanged. Pending/quarantined labels remain excluded; undefined rates do
not become zero; flagged value does not mean recovered/prevented money. Model, scoring,
backend, credential handling, and proxy security remain outside this change.

Detailed XUI component migrations of Review queue/case view, then Blocklist, Rulesets,
Tester, and Simulator are follow-ups. Their changes here are limited to the shell, tokens,
heading hierarchy, and contained tables/narrow forms. The existing Simulator refs warning
is separate. No Jira items are created or changed by this frontend migration.

## Follow-up: saved appearance and Xsolla wordmark

The `console-theme-toggle` follow-up adds the user-requested manual appearance control.
The initial visit follows system appearance. A manual light or dark choice is stored in
`anti-fraud.theme` for this browser origin and stays pinned across reloads, route changes,
and subsequent OS appearance changes. There is deliberately no System reset control.
Invalid or missing values fall back to the system; storage events synchronize other tabs.
If saving is blocked, the choice remains effective in memory and an accessible, nonblocking
message explains that it applies only for this visit. Native control/scrollbar appearance
and the color-scheme meta tag follow the same resolved XUI provider mode.

The top-right native toggle has a 64×44px target, `aria-pressed`, the stable accessible
name “Dark mode”, a sliding thumb, and sun/moon SVGs. Only the thumb and icons animate
(200ms); reduced-motion users receive an immediate state change. Page colors continue to
use XUI tokens without a full-page transition or a second theme palette.

The user-supplied Xsolla standalone wordmark is bundled as an SVG, preserving the supplied
geometry, `#31CAE8` fallback and display-P3 color. Only the escaped XML namespace was
normalized. It replaces the af square, above the retained project title/caption with 16px
clear space. The image has `alt="Xsolla"`, measures 141×38px, and adds no external link.
Sidebar, mobile header and drawer share this treatment. Mobile Menu sits below the toggle;
at very narrow widths a separate actions row places Menu left and the toggle right.
The independent-project footer and existing font authorization/delivery remain unchanged.

References checked November 12, 2026:
[official colors](https://brandbook.xsolla.com/document/28#/-/colors-1),
[official logo usage](https://brandbook.xsolla.com/document/28#/-/logo),
[localStorage behavior](https://developer.mozilla.org/en-US/docs/Web/API/Window/localStorage),
and [reduced motion](https://developer.mozilla.org/en-US/docs/Web/CSS/@media/prefers-reduced-motion).
The brandbook specifies standalone wordmarks for product UI and blue/black/white logo
colors, with darker brand blue permitted on light surfaces for legibility. No package,
backend API, authentication, account-setting, schema, or production deployment changes
are part of this follow-up. localhost and 127.0.0.1, or different ports, store preferences
separately because storage is scoped to the browser origin.

Verification adds saved/system precedence, reload, invalid/blocked storage, actual
cross-tab synchronization, keyboard/focus, reduced motion, logo geometry/clear space,
and header positioning tests. Existing all-route 390/768/1280px and half-width reflow checks,
charts, quality selection, accessibility scans and computed contrast checks remain active.
Screenshots for the follow-up use `AF_EVIDENCE_DIR=/private/tmp/console-theme-evidence`,
with the unchanged header captured under `before/` before editing. Curated comparisons
are retained under `console-theme/` alongside this note.

Follow-up validation: 24 existing Node tests and production build pass; the complete
three-browser run passes 132 tests. A final 30-case theme run also passes after moving
storage-failure feedback into normal header flow so it cannot obscure Menu. Existing
Simulator refs and bundle-size warnings remain unchanged in kind; no new lint warnings.

Comparison evidence:
[before light](console-theme/before-light-1280.png),
[after light](console-theme/after-light-1280.png),
[before dark](console-theme/before-dark-1280.png),
[after dark](console-theme/after-dark-1280.png),
[mobile light](console-theme/after-light-390.png),
[mobile dark](console-theme/after-dark-390.png),
[light contrast](console-theme/contrast-light.json),
[dark contrast](console-theme/contrast-dark.json).

Local delivery check: only the frontend container was rebuilt and became healthy at
`http://localhost:3000`; the backend and database container IDs were unchanged. The local
page showed the supplied wordmark and Backend: ready. Manual switching to light remained
selected after reload. Chrome's actual 200% zoom indicator and accessibility tree exposed
the wordmark, theme control, Menu and period controls. User interaction interrupted the
native drawer-click check at that zoom; automated all-route half-width reflow and drawer
keyboard checks passed. The temporary zoom was restored to 100% and the test tab closed.

## Follow-up: sign-in, role-based pages and full redesign

AF-136 to AF-140 replace the retained legacy styling on every page and add the console
sign-in. The XUI provider remains the only source of color, type and spacing tokens;
`src/styles/` holds one stylesheet per concern (base, controls, shell, data, pages, login)
and derives the few extra values (hover, shadow, radius scale) from those tokens. The
sidebar, buttons, status label and headings are now native elements styled with the
tokens, so the button, typography, status and sidebar XUI packages are no longer installed.

- Sign-in: a two-column screen with a fixed dark brand panel and the form; one column
  below 56rem. A wrong username, wrong password and disabled account share one message.
  A 429 shows a countdown from `Retry-After` and keeps the button from sending.
- Session: the token is kept in `localStorage` under `anti-fraud.session`, checked with
  `GET /auth/me` on load, and sent as `Authorization: Bearer`. Any 401 on a session
  request returns to sign-in with an explanation. Signing out in one tab applies to all.
- Roles: reviewers see the review queue; admins see every page, including Assignments
  and Accounts. A page outside the role shows a "not available" state.
- Review queue: a purchase is claimed before it is decided. Rows show who holds the
  claim, and the actions follow it. Below 48rem, rows of the main tables become cards
  and keep their table roles.
- The proxies add the service key only to `POST /score`.

Checks: `npm run check`, `npm run build`, and the Chromium browser suite, which now
includes sign-in, role, claim, account and assignment flows and axe scans of the
sign-in screen and every page in both themes at 1280 and 390 pixels.

## Follow-up: motion

AF-143 adds short CSS animations in `src/styles/motion.css`: the mobile drawer and its
backdrop slide and fade in both directions, buttons and navigation respond to hover and
press, the current-page marker grows into place, pages, tiles, notices and confirmations
fade in, and the sign-in screen has a short entrance. Nothing lasts longer than 280 ms and
nothing moves the layout. The whole file applies only under
`prefers-reduced-motion: no-preference`. The browser suite runs with reduced motion, so
clicks and color checks never wait for an animation; `e2e/motion.spec.ts` turns motion on.
