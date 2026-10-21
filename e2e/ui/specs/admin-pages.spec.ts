import { test, expect, loginAsAdmin, waitIdle, checkTrace } from "../lib/fixtures";

// Admin dashboard: read-only pages. Written against the reference UI (jellyfin-web 12.2).

async function openDash(page: any, path: string) {
  await loginAsAdmin(page);
  await page.goto(`/web/#/dashboard${path}`);
  await waitIdle(page);
}
const main = (page: any) => page.locator("main");

test("dashboard home shows server info, activity and devices", async ({ page, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await page.goto("/web/#/dashboard");
  await waitIdle(page);
  const m = main(page);
  await expect(m.getByText("Server name", { exact: true })).toBeVisible();
  await expect(m.getByText("Server version", { exact: true })).toBeVisible();
  await expect(m.getByRole("button", { name: "Scan All Libraries" })).toBeVisible();
  await expect(m.getByRole("button", { name: "Restart" })).toBeVisible();
  await expect(m.getByRole("button", { name: "Shutdown" })).toBeVisible();
  // library item counts from the fixture libraries
  await expect(m.getByText("Movies", { exact: true })).toBeVisible();
  await expect(m.getByText("Episodes", { exact: true })).toBeVisible();
  // activity widget contains the admin's own authentication
  await expect(m.getByText(/admin successfully authenticated/).first()).toBeVisible();
  await expect(m.getByRole("link", { name: "Activity" })).toBeVisible();
  await expect(m.getByRole("link", { name: "Devices" })).toBeVisible();
  await expect(m.getByRole("link", { name: "Paths" })).toBeVisible();
  checkTrace("dashboard-home", info.project.metadata.target, trace, since);
});

test("dashboard side menu lists the admin sections", async ({ page }) => {
  await openDash(page, "");
  for (const l of ["Dashboard", "General", "Branding", "Users", "Devices", "Activity", "Plugins", "Networking", "API Keys", "Logs", "Scheduled Tasks"]) {
    await expect(page.getByRole("link", { name: l, exact: true }).first()).toBeVisible();
  }
});

test("devices page lists the admin session", async ({ page, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await page.goto("/web/#/dashboard/devices");
  await waitIdle(page);
  await expect(page.getByRole("heading", { name: "Devices", level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "Delete All" })).toBeVisible();
  const row = page.getByRole("row").filter({ hasText: "Jellyfin Web" }).first();
  await expect(row).toBeVisible();
  await expect(row).toContainText("admin");
  checkTrace("devices", info.project.metadata.target, trace, since);
});

test("activity log lists authentication entries and filters", async ({ page, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await page.goto("/web/#/dashboard/activity");
  await waitIdle(page);
  await expect(page.getByRole("heading", { name: "Activity", level: 1 })).toBeVisible();
  await expect(page.getByRole("row").filter({ hasText: "admin successfully authenticated" }).first()).toBeVisible();
  // the System filter hides user-attributed entries; All restores them
  await page.getByRole("button", { name: "System", exact: true }).click();
  await waitIdle(page);
  await expect(page.getByRole("row").filter({ hasText: "admin successfully authenticated" })).toHaveCount(0);
  await page.getByRole("button", { name: "All", exact: true }).click();
  await expect(page.getByRole("row").filter({ hasText: "admin successfully authenticated" }).first()).toBeVisible();
  checkTrace("activity", info.project.metadata.target, trace, since);
});

test("playback transcoding settings page renders", async ({ page, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await page.goto("/web/#/dashboard/playback/transcoding");
  await waitIdle(page);
  await expect(page.getByRole("heading", { name: "Transcoding", level: 1 })).toBeVisible();
  await expect(page.getByRole("combobox", { name: /Hardware acceleration/ })).toBeVisible();
  await expect(page.getByRole("checkbox", { name: "Allow encoding in HEVC format" })).toBeVisible();
  await expect(page.getByRole("combobox", { name: /Transcoding thread count/ })).toBeVisible();
  await expect(page.getByRole("button", { name: "Save" })).toBeVisible();
  checkTrace("transcoding", info.project.metadata.target, trace, since);
});

test("networking page renders", async ({ page, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await page.goto("/web/#/dashboard/networking");
  await waitIdle(page);
  await expect(page.getByRole("heading", { name: "Networking", level: 1 })).toBeVisible();
  for (const h of ["Server Address Settings", "HTTPS Settings", "Remote Access Settings", "IP Protocols"]) {
    await expect(page.getByRole("heading", { name: h })).toBeVisible();
  }
  await expect(page.getByRole("checkbox", { name: "Enable IPv4" })).toBeChecked();
  await expect(page.getByRole("checkbox", { name: "Allow remote connections to this server" })).toBeChecked();
  checkTrace("networking", info.project.metadata.target, trace, since);
});

test("plugins page opens", async ({ page, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await page.goto("/web/#/dashboard/plugins");
  await waitIdle(page);
  await expect(page.getByRole("link", { name: "Manage Repositories" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Installed" })).toBeVisible();
  checkTrace("plugins", info.project.metadata.target, trace, since);
});

test("logs page opens and lists a log file", async ({ page, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await page.goto("/web/#/dashboard/logs");
  await waitIdle(page);
  await expect(page.getByRole("heading", { name: "Logs", level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "Save" })).toBeVisible();
  await expect(page.getByRole("heading", { level: 3 }).filter({ hasText: /\.log$/ }).first()).toBeVisible();
  checkTrace("logs", info.project.metadata.target, trace, since);
});
