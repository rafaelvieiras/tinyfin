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
  /** Distinct {method,path,status}, sorted; optionally only what happened between two mark()s. */
  unique(since = 0, until?: number): TraceEntry[] {
    const seen = new Map<string, TraceEntry>();
    for (const e of this.entries.slice(since, until)) seen.set(`${e.method} ${e.path} ${e.status}`, e);
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
