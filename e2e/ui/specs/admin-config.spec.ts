import { test, expect, loginAsAdmin, waitIdle, checkTrace, adminCall } from "../lib/fixtures";

// Admin dashboard: General and Branding settings. Every change is restored through the API afterwards.

test("general settings: server name change persists after reload", async ({ page, baseURL, apiTrace: trace }, info) => {
  const cfg = (await adminCall(baseURL!, "GET", "/System/Configuration")).json;
  const original: string = cfg.ServerName;
  const NEW = "UIT Server Name";
  try {
    await loginAsAdmin(page);
    await page.goto("/web/#/dashboard/settings");
    await waitIdle(page);
    const name = page.getByRole("textbox", { name: "Server name" });
    await expect(name).toHaveValue(original);
    const since = trace.mark();
    await name.fill(NEW);
    await page.getByRole("button", { name: "Save" }).click();
    await expect.poll(async () => (await adminCall(baseURL!, "GET", "/System/Configuration")).json.ServerName).toBe(NEW);
    await page.reload();
    await waitIdle(page);
    await expect(page.getByRole("textbox", { name: "Server name" })).toHaveValue(NEW);
    const pub = await (await fetch(new URL("/System/Info/Public", baseURL!))).json();
    expect(pub.ServerName).toBe(NEW);
    checkTrace("config-servername", info.project.metadata.target, trace, since);
  } finally {
    const cur = (await adminCall(baseURL!, "GET", "/System/Configuration")).json;
    await adminCall(baseURL!, "POST", "/System/Configuration", { ...cur, ServerName: original });
  }
});

test("branding settings: login disclaimer persists and shows on the login page", async ({ page, baseURL, apiTrace: trace }, info) => {
  const original = (await adminCall(baseURL!, "GET", "/System/Configuration/branding")).json;
  const MSG = "UIT login disclaimer";
  try {
    await loginAsAdmin(page);
    await page.goto("/web/#/dashboard/branding");
    await waitIdle(page);
    const since = trace.mark();
    await page.getByRole("textbox", { name: "Login disclaimer" }).fill(MSG);
    await page.getByRole("button", { name: "Save" }).click();
    await expect.poll(async () => (await adminCall(baseURL!, "GET", "/System/Configuration/branding")).json.LoginDisclaimer).toBe(MSG);
    await page.reload();
    await waitIdle(page);
    await expect(page.getByRole("textbox", { name: "Login disclaimer" })).toHaveValue(MSG);
    // public (unauthenticated) branding endpoint used by the login page
    const pub = await (await fetch(new URL("/Branding/Configuration", baseURL!))).json();
    expect(pub.LoginDisclaimer).toBe(MSG);
    checkTrace("config-branding", info.project.metadata.target, trace, since);
  } finally {
    await adminCall(baseURL!, "POST", "/System/Configuration/branding", original);
  }
});
