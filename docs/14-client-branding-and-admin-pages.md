# Client branding and dashboard pages of our own

**What this is.** A feasibility study, not an implementation. It records where a compatible server
can and cannot change what an official client shows, measured on 10 Nov 2026, and what each answer
costs. Nothing in jellyfin-web or jellyfin-android is modified, forked or patched by this project:
jellyfin-web stays the unmodified upstream build the image fetches (`docs/00-decision-log.md`, D9),
and the Android application stays the one users install from the store.

**Why it matters.** Tinyfin ships a server, not a client. What a user sees is upstream
jellyfin-web, plus the Android shell that loads it. Every visual customization therefore has to
arrive through the server: through a document the server answers, or through a picture the server
delivers. There is no third place to put it.

Two client facts drive everything below, both read from the clients' own sources for behavior only
(the method `docs/11-jellyfin-web-route-coverage.md` already documents):

- jellyfin-web injects the server's `BrandingOptions.CustomCss` as a `<style>` element in the two
  user-facing layouts (`src/apps/modern/AppLayout.tsx`, `src/apps/legacy/AppLayout.tsx`, via
  `src/components/CustomCss.tsx`). The **dashboard** layout
  (`src/apps/dashboard/AppLayout.tsx`) does not include that component.
- jellyfin-android is a WebView shell: `WebViewFragment.kt` calls
  `loadUrl("${server.hostname.trimEnd('/')}/")` and ships no web assets of its own. It renders our
  web bundle, so every web answer below applies to it too, with no app change.

## 1. What reaches where

| Channel | What it can change | What it cannot reach |
|---|---|---|
| `BrandingOptions.CustomCss` (`GET /Branding/Configuration`, `POST /System/Configuration/branding`) | Anything inside the user-facing document: logo, colours, typography, spacing | The dashboard (no `CustomCss` component there, measured: a clean dashboard document has no injected style) |
| `BrandingOptions.SplashscreenEnabled` + `GET /Branding/Splashscreen` | The login backdrop, web and Android | Everything else |
| A page served at `GET /web/ConfigurationPage?name=` | An admin page of our own, rendered inside the dashboard, with our HTML and our JS module | Anything outside that page's own box |
| A `<style>` injected into the served `index.html` | Anything in the document, dashboard included | Text baked into bitmap or vector assets |
| The APK itself | Launcher icon, Android 12 splash, connect-screen logo | Nothing without a fork |

## 2. A custom logo, with no client change: yes

One rule, measured in Chromium against the web bundle Tinyfin serves:

```css
img[src*="icon-transparent"] { content: url("/tinyfin-logo.svg") !important; }
```

The upstream logo is the bundled `@jellyfin/ux-web/icon-transparent.png`. Measured with that rule:
the toolbar mark (28x28 in `ServerButton`) and the drawer header mark (37x37 in
`DrawerHeaderLink`) both swap, and both keep their box exactly — the layout does not move.

The delivery path already exists: Tinyfin answers `GET /Branding/Configuration` and
`GET/POST /System/Configuration/branding`, so the CSS can be stored and served today. Since the
server generates that answer itself, it does not have to depend on an administrator pasting
anything: Tinyfin can append its own branding rules to whatever an administrator set.

Three limits, all measured or documented rather than assumed:

- **The user can turn it off.** `CustomCss` is skipped entirely when the user disables custom CSS in
  their display preferences. Branding is decoration; it must never carry a security decision.
- **`content: url()` on an `<img>` is a Chromium-family behavior.** That covers the Android WebView
  and most desktop browsers, and it is what the measurement above used. Firefox and WebKit were not
  measured. A fallback that does not depend on it: hide the picture and paint its container
  (`img[src*="icon-transparent"] { display: none }` plus
  `:has(img[src*="icon-transparent"])` on the container with an explicit `width`/`height` and a
  `background-image`) — measured to need the explicit size, because the container's own height
  collapses to zero once the picture is hidden.
- **The launcher icon, the Android 12 splash and the connect-screen logo are compiled into the
  APK** (`app_logo.xml`, `ic_splash.xml`). Out of reach without forking the app.

### 2.1 What was implemented instead: the server answers the pictures itself

The `CustomCss` rule above needs the administrator's branding to be on and the user's custom-CSS
preference (also in their display settings) to be left alone, and it reaches only `<img>` elements.
It cannot touch the favicon, the web-app icons the manifest lists, the splash mark that is a CSS
background, or the icon a desktop notification carries — none of them is an `<img>` in the
document. Measured 22/nov/2026 on the bundle Tinyfin ships (2,364 files), the client asks for
exactly five icon-only pictures, all with no lettering:

| Path the client requests | What draws it |
|---|---|
| `favicon.<fingerprint>.ico` | the browser tab |
| `touchicon.<fingerprint>.png` | `<link rel="apple-touch-icon">`, 180x180 |
| `touchicon144.<fingerprint>.png`, `favicons/touchicon{72,114,144,512}.png` | `manifest.json`, installed web app |
| `icon-transparent.<fingerprint>.png` | the splash mark, and the drawer and server-button marks |
| `notificationicon.<fingerprint>.png` | the icon of a desktop notification |

Those five are answered with the project's mascot by `internal/webicons`
(`DIV-39` in [`13`](13-divergences.md)); `banner-light` and `banner-dark` — the only two pictures
that carry the "Jellyfin" wordmark, drawn on the login page and beside the drawer's name — keep the
bytes the bundle ships. The fingerprints change with every upstream build, so the match is on the
file stem and the server never needs to know a hash.

Worth doing in the same pass: `SplashscreenEnabled` is served (`false` by default) but
`GET /Branding/Splashscreen` is a 404 on Tinyfin. Flipping the flag on today asks jellyfin-web to
draw a backdrop it cannot fetch. The reference exposes the whole family — `GET`, `POST` and
`DELETE /Branding/Splashscreen` (upload is a raw body, 204 back), per its own OpenAPI document. The
same picture is the login backdrop in web and in Android.

## 3. An admin section of our own, with subsections: yes, through the plugin page surface

The dashboard renders HTML **served by the server**. That is how a plugin's configuration page has
always worked, and the route is in the reference's own OpenAPI document: `GET /web/ConfigurationPages`
and `GET /web/ConfigurationPage` (both with `404` and `503` among their answers).

The chain, confirmed end to end against a live session (a throwaway proxy in front of a staging
server answered the two routes; no client code, no bundle rebuild):

1. The dashboard asks `GET /web/ConfigurationPages?enableInMainMenu=true`.
2. Each entry becomes a drawer item: `{"Name":"tinyfin","DisplayName":"Tinyfin","EnableInMainMenu":true,"PluginId":"<any guid>"}`
   put a **"Tinyfin"** item in the dashboard drawer.
3. Clicking it navigates to `#/configurationpage?name=tinyfin`; the client logs
   `[viewContainer] tryRestoreView {url: /configurationpage?name=tinyfin}` and requests
   `GET /web/configurationpage?name=tinyfin`.
4. That answer is a fragment carrying a controller: `<div data-role="page" id="tinyfinAdminPage" data-controller="__plugin/tinyfin.js">`.
5. The client resolves the controller as `__plugin/tinyfin.js` and imports it — a plain
   `export default function (view) { ... }` module. Its code ran (`console` line from the injected
   module) and the page rendered inside the dashboard chrome.

Subsections are ours to arrange: tabs inside the one page, or one entry per subsection, each its own
`.js` module. Nothing in the server needs a plugin registry for this; it needs two routes and two
templates.

Caveats to accept before building it:

- The entry lands under the drawer's **Plugins** heading, not under Server. It is where the client
  puts every server-supplied page.
- The page is a legacy surface: our own HTML and CSS, not the dashboard's MUI components. Plain
  form controls work; the dashboard's React components and theme are not available to it.
- `GET /web/ConfigurationPage` must be answered before the static file server, exactly as the
  existing `GET /web/ConfigurationPages` special case in `cmd/tinyfin/webui.go` already is.
  Otherwise the file server answers 404 for the path with or without a web bundle mounted.

## 4. Hiding dashboard entries we do not implement: no server-side lever

Two negative results, both measured on a clean dashboard document:

- The dashboard does not apply the server's `CustomCss` (`hasCustomCss: false` in the live document).
- No server answer suppresses a drawer item. The drawer's sections are unconditional in
  `src/apps/dashboard/components/drawer/sections/*.tsx`; what varies with the server is the *content*
  of a page, not the presence of its menu entry.

Hiding one is therefore a document change, and the only place Tinyfin can make it without touching
upstream files is the `index.html` it serves (the file server is ours, `cmd/tinyfin/webui.go`). Two
selectors, measured:

| Rule | Measured result |
|---|---|
| `a[href="#/dashboard/plugins"] { display: none }` | The link goes away and leaves a **56 px hole** — the section keeps its heading and its spacing |
| `ul:has(a[href="#/dashboard/plugins"]) { display: none }` | The whole list goes away: heading "Plugins" **and** item, box height 0 |

So the working rule is the `:has()` one on the list element. This is a real trade-off, worth stating
plainly: it is not a change to jellyfin-web's code, but it is a change to the document the client
receives, and it depends on upstream markup (a drawer item that is an `<a>` directly inside a `<ul>`
whose `href` is the dashboard route). Such a rule deserves a browser test that fails when upstream
moves, rather than a comment asking people to remember.

## 5. Out of reach without forking a client

- Launcher icon, Android 12 splash screen, connect-screen logo (compiled into the APK).
- The native player's chrome (ExoPlayer/Media3 surface) and the video player's own overlay, which
  jellyfin-web draws with its own CSS — reachable only as far as `CustomCss` can select it.
- Anything inside a native dialog the app builds itself.

## 6. What Tinyfin would have to add, and what it costs

| Work | Effort | Risk |
|---|---|---|
| Serve our branding rules from `GET /Branding/Configuration` (ours appended to the admin's) | hours | Low. `content: url()` unmeasured on Firefox/WebKit; fallback rule known |
| `GET`/`POST`/`DELETE /Branding/Splashscreen` + the flag made honest | hours | Low |
| `GET /web/ConfigurationPage` + one Tinyfin admin page with subsections | 1-2 days for a real page | Low; legacy surface, drawer placement fixed by the client |
| Inject a `<style>` into the served `index.html` to hide unimplemented dashboard entries (and carry the logo into the dashboard) | hours + a browser test | Medium: couples the answer to upstream markup |

The recommended order is the table's order: the first two are strictly server-side additions with no
coupling to client internals, the third is a documented surface of the reference, and the fourth is
the only one that can break when upstream changes.

## 7. How these answers were measured

- **Web client:** a real jellyfin-web build, served by Tinyfin's own staging deployment, driven by
  Playwright in Chromium. The logo rule was applied to a signed-in session and compared pixel-wise
  against the same screenshot without it (diff box exactly the logo's box, 28x28 and 37x37).
- **Dashboard injection:** a fresh document at `#/dashboard`, then the same CSS appended as a
  `<style>` element, checking `display`/box of the Plugins entry and of its `<ul>`.
- **Configuration page surface:** a throwaway reverse proxy in front of the staging server answered
  `GET /web/ConfigurationPages` and `GET /web/ConfigurationPage`, leaving everything else untouched,
  so the client under test was the real one with a real login. The session's own console and network
  log are quoted above.
- **Server side:** `curl` against the live deployment and against the reference, comparing the same
  routes (`/Branding/Configuration`, `/Branding/Css`, `/Branding/Splashscreen`,
  `/System/Configuration/{branding,metadata,xbmcmetadata}`, `/web/ConfigurationPage(s)`). The
  reference's own OpenAPI document supplied each route's declared parameters and answers.
- No client source was read for anything beyond which component renders what and which route it
  calls. No upstream file was copied.
