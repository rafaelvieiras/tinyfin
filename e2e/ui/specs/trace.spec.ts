import { mkdirSync, readFileSync, writeFileSync, existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { test, expect, loginAsAdmin, waitIdle, openItem, type TraceEntry } from "../lib/fixtures";

// Trace-diff: record the normalized API trace of home -> library -> detail.
//  - UPDATE_GOLDEN=1 (reference only, via `run.sh --update-golden`) rewrites golden/home-library-detail.json.
//  - ref (normal run): the live trace must still cover the golden endpoints (guards golden drift).
//  - tinyfin: no endpoint present in golden may be missing, and no response may be 4xx/5xx
//    where the reference answered with a success.
const GOLDEN = join(__dirname, "..", "golden", "home-library-detail.json");
const key = (e: { method: string; path: string }) => `${e.method} ${e.path}`;

test("api trace home -> library -> detail matches the reference", async ({ page, apiTrace: trace }, info) => {
  const target = info.project.metadata.target as string;
  await loginAsAdmin(page);
  const since = trace.mark();
  await page.goto("/web/#/home");
  await waitIdle(page);
  await page.locator(".card").filter({ hasText: "Movies" }).first().click();
  await expect(page.locator(".card").filter({ hasText: "Alpha Movie" }).first()).toBeVisible();
  await waitIdle(page);
  await openItem(page, "Alpha Movie");
  await expect(page.locator("button.btnPlay").first()).toBeVisible();
  await waitIdle(page);
  const got = trace.unique(since);

  if (process.env.UPDATE_GOLDEN === "1") {
    expect(target, "golden may only be generated against the reference").toBe("ref");
    mkdirSync(dirname(GOLDEN), { recursive: true });
    writeFileSync(GOLDEN, JSON.stringify({ flow: "home -> Movies -> Alpha Movie", entries: got }, null, 2) + "\n");
    return;
  }
  expect(existsSync(GOLDEN), "golden missing: run `run.sh --update-golden` against the reference").toBe(true);
  const golden: TraceEntry[] = JSON.parse(readFileSync(GOLDEN, "utf8")).entries;
  const gotKeys = new Set(got.map(key));
  const missing = [...new Set(golden.map(key))].filter((k) => !gotKeys.has(k));
  expect(missing, "endpoints the reference UI calls that this server run did not").toEqual([]);
  if (target === "tinyfin") {
    const refOk = new Set(golden.filter((e) => e.status < 400).map(key));
    const bad = got.filter((e) => e.status >= 400 && refOk.has(key(e))).map((e) => `${key(e)} -> ${e.status}`);
    expect(bad, "responses that fail here but succeed on the reference").toEqual([]);
    const refBad = new Set(golden.filter((e) => e.status >= 400).map(key));
    const extra = got.filter((e) => e.status >= 400 && !refBad.has(key(e)) && !refOk.has(key(e))).map((e) => `${key(e)} -> ${e.status}`);
    expect(extra, "4xx/5xx on endpoints the reference never called").toEqual([]);
  }
});
