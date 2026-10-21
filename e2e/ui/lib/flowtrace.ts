import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { expect, type TraceEntry } from "./fixtures";

const key = (e: { method: string; path: string }) => `${e.method} ${e.path}`;
// Image bytes and per-session noise vary between runs; they are not part of the flow contract.
const NOISE = [/\/Images\//, /^\/Sessions\/Capabilities/, /^\/QuickConnect/, /^\/Branding/];
const relevant = (e: TraceEntry) => !NOISE.some((re) => re.test(e.path));

/**
 * Golden API trace for one user flow. ref + UPDATE_GOLDEN=1 writes golden/<name>.json.
 * Otherwise the run must still cover the golden endpoints, and (tinyfin) must not answer
 * 4xx/5xx where the reference answered with a success or never called the endpoint.
 */
export function checkFlowTrace(name: string, target: string, got: TraceEntry[]) {
  const file = join(__dirname, "..", "golden", `${name}.json`);
  const mine = got.filter(relevant);
  if (process.env.UPDATE_GOLDEN === "1") {
    expect(target, "golden may only be generated against the reference").toBe("ref");
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, JSON.stringify({ flow: name, entries: mine }, null, 2) + "\n");
    return;
  }
  expect(existsSync(file), `golden missing: run TARGET=ref ./run.sh --update-golden`).toBe(true);
  const golden: TraceEntry[] = JSON.parse(readFileSync(file, "utf8")).entries;
  const have = new Set(mine.map(key));
  const missing = [...new Set(golden.filter((e) => e.status < 400).map(key))].filter((k) => !have.has(k));
  expect(missing, "endpoints the reference UI called that this run did not").toEqual([]);
  const refOk = new Set(golden.filter((e) => e.status < 400).map(key));
  const refBad = new Set(golden.filter((e) => e.status >= 400).map((e) => `${key(e)} ${e.status}`));
  const bad = mine
    .filter((e) => e.status >= 400 && !refBad.has(`${key(e)} ${e.status}`))
    .map((e) => `${key(e)} -> ${e.status}${refOk.has(key(e)) ? " (reference: success)" : " (reference never failed here)"}`);
  expect(bad, "4xx/5xx not seen on the reference").toEqual([]);
}
