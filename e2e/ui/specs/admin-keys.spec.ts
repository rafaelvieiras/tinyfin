import { test, expect, loginAsAdmin, waitIdle, checkTrace, adminCall } from "../lib/fixtures";

// Admin dashboard: API keys (create, list, revoke). Keys created here carry the app name "UIT Key".
const APP = "UIT Key";
const keysNamed = async (baseURL: string) =>
  ((await adminCall(baseURL, "GET", "/Auth/Keys")).json.Items as { AccessToken: string; AppName: string }[]).filter((k) => k.AppName === APP);
async function cleanup(baseURL: string) {
  for (const k of await keysNamed(baseURL)) await adminCall(baseURL, "DELETE", `/Auth/Keys/${k.AccessToken}`);
}
test.beforeEach(async ({ baseURL }) => { await cleanup(baseURL!); });
test.afterEach(async ({ baseURL }) => { await cleanup(baseURL!); });

async function createKey(page: any) {
  await page.goto("/web/#/dashboard/keys");
  await waitIdle(page);
  await page.getByRole("button", { name: "New API Key" }).click();
  await page.getByRole("dialog", { name: "New API Key" }).getByRole("textbox", { name: "App name" }).fill(APP);
  await page.getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("row").filter({ hasText: APP })).toBeVisible();
}

test("api keys page renders", async ({ page, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await page.goto("/web/#/dashboard/keys");
  await waitIdle(page);
  await expect(page.getByRole("heading", { name: "API Keys", level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "New API Key" })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: /^App/ })).toBeVisible();
  checkTrace("keys-list", info.project.metadata.target, trace, since);
});

test("create an API key and use it", async ({ page, baseURL, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await createKey(page);
  const keys = await keysNamed(baseURL!);
  expect(keys).toHaveLength(1);
  await expect(page.getByRole("row").filter({ hasText: APP })).toContainText(keys[0].AccessToken);
  // the key authenticates API requests
  const r = await fetch(new URL("/System/Info", baseURL!), { headers: { Authorization: `MediaBrowser Token="${keys[0].AccessToken}"` } });
  expect(r.status).toBe(200);
  checkTrace("keys-create", info.project.metadata.target, trace, since);
});

test("revoke an API key", async ({ page, baseURL, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  await createKey(page);
  const [key] = await keysNamed(baseURL!);
  const since = trace.mark();
  await page.getByRole("row").filter({ hasText: APP }).getByRole("button", { name: "Revoke" }).click();
  await page.getByRole("dialog", { name: "Revoke API Key" }).getByRole("button", { name: "Delete" }).click();
  await expect(page.getByRole("row").filter({ hasText: APP })).toHaveCount(0);
  expect(await keysNamed(baseURL!)).toHaveLength(0);
  const r = await fetch(new URL("/System/Info", baseURL!), { headers: { Authorization: `MediaBrowser Token="${key.AccessToken}"` } });
  expect(r.status).toBe(401);
  checkTrace("keys-revoke", info.project.metadata.target, trace, since);
});
