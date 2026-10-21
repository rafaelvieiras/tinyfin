import { mkdirSync, readFileSync, writeFileSync, existsSync } from "node:fs";
import { join } from "node:path";
import { test as base, expect, type Page, type Response } from "@playwright/test";

export { expect };

const ADMIN_USER = process.env.UI_ADMIN_USER ?? "admin";
const ADMIN_PASS = process.env.UI_ADMIN_PASS ?? "Conformance-Admin-1";

export interface TraceEntry {
  method: string;
  path: string;
  status: number;
}

const GUID = /\b[0-9a-f]{8}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{12}\b/gi;

/** Replaces ids with a placeholder so traces compare across servers (ids differ per server). */
export function normalizePath(p: string): string {
  return p.replace(GUID, ":id").replace(/\/\d{3,}(?=\/|$)/g, "/:n");
}

// Static client assets and media bytes are not API surface.
const IGNORED = [/^\/web\//, /^\/favicon/, /^\/(Videos|Audio)\//, /^\/socket/];

/** Records the normalized API trace (method + path + status) of one page. */
export class ApiTrace {
  private entries: TraceEntry[] = [];
  constructor(private origin: string) {}
  attach(page: Page) {
    page.on("response", (r: Response) => {
      const u = new URL(r.url());
      if (u.origin !== this.origin) return;
      if (IGNORED.some((re) => re.test(u.pathname))) return;
      this.entries.push({ method: r.request().method(), path: normalizePath(u.pathname), status: r.status() });
    });
  }
  mark() { return this.entries.length; }
  /** Distinct {method,path,status}, sorted; optionally only what happened after mark(). */
  unique(since = 0): TraceEntry[] {
    const seen = new Map<string, TraceEntry>();
    for (const e of this.entries.slice(since)) seen.set(`${e.method} ${e.path} ${e.status}`, e);
    return [...seen.values()].sort((a, b) => (a.path + a.method + a.status).localeCompare(b.path + b.method + b.status));
  }
}

export async function waitIdle(page: Page, ms = 15_000) {
  // jellyfin-web keeps a websocket and image requests flowing; idle may never be reached, so best effort.
  await page.waitForLoadState("networkidle", { timeout: ms }).catch(() => {});
}

/** Logs in through the UI; works whether or not the server lists public users on the login page. */
export async function loginAsAdmin(page: Page) {
  await page.goto("/web/");
  const manual = page.locator(".btnManual").first();
  const name = page.locator("#txtManualName");
  await expect(page.locator("#txtManualName:visible, .btnManual:visible").first()).toBeVisible();
  if (!(await name.isVisible())) await manual.click();
  await name.fill(ADMIN_USER);
  await page.locator("#txtManualPassword").fill(ADMIN_PASS);
  await page.locator("button.button-submit").click();
  await expect(page).toHaveURL(/#\/home/);
  await waitIdle(page);
}

/** Admin access token and user id through the public API, for locating fixture ids. */
export async function adminApi(baseURL: string) {
  const auth = 'MediaBrowser Client="tinyfin-ui", Device="spec", DeviceId="tinyfin-ui-spec", Version="1.0"';
  const r = await fetch(new URL("/Users/AuthenticateByName", baseURL), {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: auth },
    body: JSON.stringify({ Username: ADMIN_USER, Pw: ADMIN_PASS }),
  });
  if (!r.ok) throw new Error(`admin API login failed: HTTP ${r.status}`);
  const j = (await r.json()) as { AccessToken: string };
  return async (path: string) => {
    const x = await fetch(new URL(path, baseURL), { headers: { Authorization: `${auth}, Token="${j.AccessToken}"` } });
    return x.json() as Promise<any>;
  };
}

/** A card (library tile or item) containing the text. */
export const card = (page: Page, text: string) => page.locator(".card").filter({ hasText: text }).first();
/** Clicking the title link opens the details page; clicking the card image would start playback. */
export const openItem = (page: Page, title: string) => card(page, title).locator(".cardText a").first().click();

export const test = base.extend<{ apiTrace: ApiTrace }>({
  apiTrace: async ({ page, baseURL }, use) => {
    const t = new ApiTrace(new URL(baseURL!).origin);
    t.attach(page);
    await use(t);
  },
});

const GOLDEN_DIR = join(__dirname, "..", "golden");
const tkey = (e: { method: string; path: string }) => `${e.method} ${e.path}`;

/**
 * Golden API-trace check for one admin flow (name -> golden/admin-<name>.json).
 *  - UPDATE_GOLDEN=1 on the reference: rewrites the golden.
 *  - any server: no 5xx at all.
 *  - tinyfin: no endpoint that the reference called may be missing, and no 4xx/5xx where the
 *    reference answered with a success (or on endpoints the reference never called).
 */
export function checkTrace(name: string, target: string, trace: ApiTrace, since: number) {
  const file = join(GOLDEN_DIR, `admin-${name}.json`);
  const got = trace.unique(since);
  if (process.env.UPDATE_GOLDEN === "1") {
    expect(target, "golden may only be generated against the reference").toBe("ref");
    mkdirSync(GOLDEN_DIR, { recursive: true });
    writeFileSync(file, JSON.stringify({ flow: name, entries: got }, null, 2) + "\n");
    return;
  }
  expect(got.filter((e) => e.status >= 500).map((e) => `${tkey(e)} -> ${e.status}`), "5xx responses").toEqual([]);
  expect(existsSync(file), `golden missing: TARGET=ref ./run.sh --update-golden`).toBe(true);
  const golden: TraceEntry[] = JSON.parse(readFileSync(file, "utf8")).entries;
  const gotKeys = new Set(got.map(tkey));
  // Artwork requests depend on whether the server produced images for the fixtures, not on the admin UI.
  const isImage = (k: string) => /\/Images?(\/|$)/.test(k);
  expect([...new Set(golden.map(tkey))].filter((k) => !gotKeys.has(k) && !isImage(k)), "endpoints the reference UI calls that this run did not").toEqual([]);
  if (target === "tinyfin") {
    const refOk = new Set(golden.filter((e) => e.status < 400).map(tkey));
    const refBad = new Set(golden.filter((e) => e.status >= 400).map(tkey));
    const bad = got.filter((e) => e.status >= 400 && !refBad.has(tkey(e))).map((e) => `${tkey(e)} -> ${e.status}${refOk.has(tkey(e)) ? " (ref ok)" : " (ref never called)"}`);
    expect(bad, "4xx/5xx that the reference did not produce").toEqual([]);
  }
}

/** Generic admin API call (any method) for set-up, verification and clean-up of UI-driven mutations. */
export async function adminCall(baseURL: string, method: string, path: string, body?: unknown, token?: { user: string; pass: string }) {
  const auth = 'MediaBrowser Client="tinyfin-ui", Device="spec", DeviceId="tinyfin-ui-spec", Version="1.0"';
  const login = await fetch(new URL("/Users/AuthenticateByName", baseURL), {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: auth },
    body: JSON.stringify({ Username: token?.user ?? ADMIN_USER, Pw: token?.pass ?? ADMIN_PASS }),
  });
  if (!login.ok) throw new Error(`API login failed: HTTP ${login.status}`);
  const t = ((await login.json()) as { AccessToken: string }).AccessToken;
  const r = await fetch(new URL(path, baseURL), {
    method,
    headers: { Authorization: `${auth}, Token="${t}"`, ...(body !== undefined ? { "Content-Type": "application/json" } : {}) },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await r.text();
  let json: any = null;
  try { json = text ? JSON.parse(text) : null; } catch { /* not JSON */ }
  return { status: r.status, json };
}

/** Deletes every user whose name starts with the prefix (clean-up for UI-created users). */
export async function deleteUsersByPrefix(baseURL: string, prefix: string) {
  const users = (await adminCall(baseURL, "GET", "/Users")).json as { Id: string; Name: string }[];
  for (const u of users.filter((x) => x.Name.startsWith(prefix))) await adminCall(baseURL, "DELETE", `/Users/${u.Id}`);
}
