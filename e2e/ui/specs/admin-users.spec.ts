import { test, expect, loginAsAdmin, waitIdle, checkTrace, adminCall, deleteUsersByPrefix } from "../lib/fixtures";

// Admin dashboard: user management through the UI. Every spec creates its own "uit-" users and
// removes them again (also on failure), so the specs are order independent.
const PREFIX = "uit-";
const PW1 = "Uit-Pass-1";
const PW2 = "Uit-Pass-2";

test.beforeEach(async ({ baseURL }) => { await deleteUsersByPrefix(baseURL!, PREFIX); });
test.afterEach(async ({ baseURL }) => { await deleteUsersByPrefix(baseURL!, PREFIX); });

async function addUserViaUi(page: any, name: string, pass: string) {
  await page.goto("/web/#/dashboard/users/add");
  await page.getByRole("textbox", { name: "Name" }).fill(name);
  await page.getByRole("textbox", { name: "Password" }).fill(pass);
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page).toHaveURL(/#\/dashboard\/users\/[0-9a-f]+\/profile/);
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
}
/** emby-checkbox inputs are covered by their label: toggle through the label and verify the state. */
async function setBox(box: any, on: boolean) {
  if ((await box.isChecked()) === on) return;
  await box.locator("xpath=..").click();
  if (on) await expect(box).toBeChecked(); else await expect(box).not.toBeChecked();
}
const userCard = (page: any, name: string) => page.locator(`[data-username="${name}"]`);

test("users list shows the admin", async ({ page, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await page.goto("/web/#/dashboard/users");
  await waitIdle(page);
  await expect(page.getByRole("heading", { name: "Users", level: 2 })).toBeVisible();
  await expect(page.getByRole("button", { name: "Add User" })).toBeVisible();
  await expect(userCard(page, "admin")).toBeVisible();
  checkTrace("users-list", info.project.metadata.target, trace, since);
});

test("create a user in the UI and see it in the list", async ({ page, baseURL, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await addUserViaUi(page, "uit-create", PW1);
  await page.goto("/web/#/dashboard/users");
  await waitIdle(page);
  await expect(userCard(page, "uit-create")).toBeVisible();
  const users = (await adminCall(baseURL!, "GET", "/Users")).json as { Name: string; HasPassword: boolean }[];
  const u = users.find((x) => x.Name === "uit-create");
  expect(u?.HasPassword).toBe(true);
  checkTrace("users-create", info.project.metadata.target, trace, since);
});

test("edit user policy toggles and persist across reload", async ({ page, baseURL, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  await addUserViaUi(page, "uit-policy", PW1);
  const since = trace.mark();
  const coll = page.getByRole("checkbox", { name: "Allow this user to manage collections" });
  const subs = page.getByRole("checkbox", { name: "Allow this user to edit subtitles" });
  const live = page.getByRole("checkbox", { name: "Allow Live TV access" });
  await expect(coll).not.toBeChecked();
  await expect(live).toBeChecked();
  await setBox(coll, true);
  await setBox(subs, true);
  await setBox(live, false);
  const id = page.url().match(/users\/([0-9a-f]+)\/profile/)![1];
  await page.getByRole("button", { name: "Save" }).click();
  await waitIdle(page);
  const pol = (await adminCall(baseURL!, "GET", `/Users/${id}`)).json.Policy;
  expect(pol.EnableCollectionManagement).toBe(true);
  expect(pol.EnableSubtitleManagement).toBe(true);
  expect(pol.EnableLiveTvAccess).toBe(false);
  expect(pol.IsAdministrator).toBe(false);
  await page.goto(`/web/#/dashboard/users/${id}/profile`);
  await page.reload();
  await waitIdle(page);
  await expect(page.getByRole("checkbox", { name: "Allow this user to manage collections" })).toBeChecked();
  await expect(page.getByRole("checkbox", { name: "Allow this user to edit subtitles" })).toBeChecked();
  await expect(page.getByRole("checkbox", { name: "Allow Live TV access" })).not.toBeChecked();
  checkTrace("users-policy", info.project.metadata.target, trace, since);
});

test("set and reset a user password", async ({ page, baseURL, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  await addUserViaUi(page, "uit-pass", PW1);
  const since = trace.mark();
  await page.getByRole("tab", { name: "Password" }).click();
  await page.getByRole("textbox", { name: "Current password" }).fill(PW1);
  await page.getByRole("textbox", { name: "New password", exact: true }).fill(PW2);
  await page.getByRole("textbox", { name: "New password confirm" }).fill(PW2);
  await page.getByRole("button", { name: "Save Password" }).click();
  await waitIdle(page);
  // old password no longer works, new one does
  const loginStatus = (pass: string) => adminCall(baseURL!, "GET", "/Users/Me", undefined, { user: "uit-pass", pass }).then((r) => r.status, () => 401);
  await expect.poll(() => loginStatus(PW2)).toBe(200);
  expect(await loginStatus(PW1)).toBe(401);
  // reset clears the password
  await page.reload();
  await waitIdle(page);
  await page.getByRole("tab", { name: "Password" }).click();
  await page.getByRole("button", { name: "Reset Password" }).click();
  await page.locator(".dialog button").filter({ hasText: /^OK$/ }).first().click();
  // after the reset the account signs in with an empty password and the old one is rejected
  await expect.poll(() => loginStatus("")).toBe(200);
  expect(await loginStatus(PW2)).toBe(401);
  checkTrace("users-password", info.project.metadata.target, trace, since);
});

test("delete a user from the list", async ({ page, baseURL, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  await addUserViaUi(page, "uit-delete", PW1);
  await page.goto("/web/#/dashboard/users");
  await waitIdle(page);
  const since = trace.mark();
  await userCard(page, "uit-delete").locator("button").click();
  await page.locator(".actionSheetMenuItem").filter({ hasText: "Delete" }).click();
  await expect(page.locator(".dialog").filter({ hasText: "Are you sure you wish to delete this user?" }).first()).toBeVisible();
  await page.locator(".dialog button").filter({ hasText: /^Delete$/ }).click();
  await expect(userCard(page, "uit-delete")).toHaveCount(0);
  const users = (await adminCall(baseURL!, "GET", "/Users")).json as { Name: string }[];
  expect(users.some((x) => x.Name === "uit-delete")).toBe(false);
  checkTrace("users-delete", info.project.metadata.target, trace, since);
});
