// Baseline ratchet for the UI battery (analogous to docs/contract/baseline.json).
//   node ratchet.mjs [results.json] [--print-union]
// baseline.json: {"version":1,"target":"tinyfin","passing":["<file> > <title>", ...]}
// Exit 1 when a baselined spec did not pass on the tinyfin project. Failures that are not
// baselined are reported as known divergences (exit 0). This script never writes baseline.json:
// with --print-union it prints baseline ∪ newly-passing to stdout for a human to review and
// commit by hand.
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const args = process.argv.slice(2);
const printUnion = args.includes("--print-union");
const resultsPath = args.find((a) => !a.startsWith("--")) ?? "results.json";
const baselinePath = join(dirname(fileURLToPath(import.meta.url)), "baseline.json");

const results = JSON.parse(readFileSync(resultsPath, "utf8"));
const baseline = JSON.parse(readFileSync(baselinePath, "utf8"));
const must = new Set(baseline.passing);

const status = new Map(); // key -> "passed" | "failed" | ...
(function walk(suite, file) {
  const f = suite.file ?? file;
  for (const spec of suite.specs ?? []) {
    for (const t of spec.tests ?? []) {
      if (t.projectName !== "tinyfin") continue;
      const last = t.results?.[t.results.length - 1];
      status.set(`${f} > ${spec.title}`, last?.status ?? t.status ?? "skipped");
    }
  }
  for (const s of suite.suites ?? []) walk(s, f);
})({ suites: results.suites }, "");

if (status.size === 0) { console.log("ratchet: no tinyfin results in " + resultsPath + " (nothing to compare)"); process.exit(0); }

const regress = [], diverge = [], fresh = [];
for (const [k, s] of status) {
  const ok = s === "passed";
  if (must.has(k) && !ok) regress.push(`${k}  [${s}]`);
  else if (!must.has(k) && !ok) diverge.push(`${k}  [${s}]`);
  else if (!must.has(k) && ok) fresh.push(k);
}
for (const k of must) if (!status.has(k)) regress.push(`${k}  [not run]`);

console.log(`ratchet: ${status.size} tinyfin specs, ${must.size} baselined`);
if (diverge.length) console.log("known divergences (not baselined, failing):\n  " + diverge.join("\n  "));
if (fresh.length) console.log("newly passing, not yet baselined (add by hand: --print-union):\n  " + fresh.join("\n  "));
if (printUnion) {
  const union = [...new Set([...must, ...fresh])].sort();
  console.log(JSON.stringify({ ...baseline, passing: union }, null, 2));
}
if (regress.length) { console.error("REGRESSIONS (baselined specs not passing):\n  " + regress.join("\n  ")); process.exit(1); }
console.log("ratchet: ok");
