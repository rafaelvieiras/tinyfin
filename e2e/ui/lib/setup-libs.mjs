// Creates the Movies and Shows libraries through the public API and waits for
// the scan, so both servers expose equivalent content to the UI.
//   node setup-libs.mjs <base-url> <server-side-media-root>
// Credentials come from UI_ADMIN_USER / UI_ADMIN_PASS (never printed).
const [base, root] = process.argv.slice(2);
if (!base || !root) { console.error("usage: setup-libs.mjs <base> <media-root>"); process.exit(2); }
const user = process.env.UI_ADMIN_USER ?? "admin";
const pass = process.env.UI_ADMIN_PASS ?? "Conformance-Admin-1";
const AUTH = 'MediaBrowser Client="tinyfin-ui", Device="setup", DeviceId="tinyfin-ui-setup", Version="1.0"';

const safe = (t) => { try { return JSON.parse(t); } catch { return null; } };
async function call(method, path, { token, body, query } = {}) {
  const url = new URL(path, base);
  for (const [k, v] of Object.entries(query ?? {})) url.searchParams.set(k, v);
  const headers = { Authorization: AUTH + (token ? `, Token="${token}"` : "") };
  if (body) headers["Content-Type"] = "application/json";
  const r = await fetch(url, { method, headers, body: body ? JSON.stringify(body) : undefined });
  const text = await r.text();
  return { status: r.status, json: text ? safe(text) : null };
}

const auth = await call("POST", "/Users/AuthenticateByName", { body: { Username: user, Pw: pass } });
if (auth.status !== 200) { console.error("admin login failed: HTTP " + auth.status); process.exit(1); }
const token = auth.json.AccessToken;

const libs = [
  { name: "Movies", type: "movies", dir: "Movies", expect: { Movie: 3 } },
  { name: "Shows", type: "tvshows", dir: "Shows", expect: { Series: 1, Episode: 3 } },
];
const have = await call("GET", "/Library/VirtualFolders", { token });
const names = new Set((have.json ?? []).map((v) => v.Name.toLowerCase()));
for (const l of libs) {
  if (names.has(l.name.toLowerCase())) continue;
  const r = await call("POST", "/Library/VirtualFolders", {
    token,
    query: { name: l.name, collectionType: l.type, refreshLibrary: "true" },
    body: { LibraryOptions: { PathInfos: [{ Path: `${root.replace(/\/$/, "")}/${l.dir}` }] } },
  });
  if (r.status !== 200 && r.status !== 204) { console.error(`create ${l.name}: HTTP ${r.status}`); process.exit(1); }
}
await call("POST", "/Library/Refresh", { token });

const deadline = Date.now() + 120_000;
for (;;) {
  let ok = true;
  for (const l of libs) {
    for (const [t, n] of Object.entries(l.expect)) {
      const r = await call("GET", "/Items", { token, query: { Recursive: "true", IncludeItemTypes: t, Limit: "1", EnableTotalRecordCount: "true" } });
      if ((r.json?.TotalRecordCount ?? 0) < n) ok = false;
    }
  }
  if (ok) break;
  if (Date.now() > deadline) { console.error("scan did not finish within 120s"); process.exit(1); }
  await new Promise((r) => setTimeout(r, 2000));
}
console.log(`libraries ready on ${base}`);
