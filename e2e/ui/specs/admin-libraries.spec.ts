import { test, expect, loginAsAdmin, waitIdle, checkTrace, adminCall } from "../lib/fixtures";

// Admin dashboard: library management through the UI wizard. Test libraries are named "UIT Lib*"
// and removed again (also on failure) so the specs are order independent.
const NAME = "UIT Lib";
const RENAMED = "UIT Lib Renamed";

async function cleanup(baseURL: string) {
  const vf = (await adminCall(baseURL, "GET", "/Library/VirtualFolders")).json as { Name: string }[];
  for (const v of vf.filter((x) => x.Name.startsWith(NAME))) {
    await adminCall(baseURL, "DELETE", `/Library/VirtualFolders?name=${encodeURIComponent(v.Name)}&refreshLibrary=true`);
  }
}
test.beforeEach(async ({ baseURL }) => { await cleanup(baseURL!); });
test.afterEach(async ({ baseURL }) => { await cleanup(baseURL!); });

/** A server-side folder that exists on both servers: a sibling of the Movies library folder. */
async function extraMediaPath(baseURL: string) {
  const vf = (await adminCall(baseURL, "GET", "/Library/VirtualFolders")).json as { Name: string; Locations: string[] }[];
  const movies = vf.find((v) => v.Name === "Movies")!.Locations[0];
  return movies.replace(/\/Movies\/?$/, "") + "/ArtShows";
}
const libMenu = (page: any, name: string) =>
  page.locator("#mediaLibraryPage p").getByText(name, { exact: true }).first().locator("xpath=../following-sibling::div//button");
const libraryNames = async (baseURL: string) =>
  ((await adminCall(baseURL, "GET", "/Library/VirtualFolders")).json as { Name: string }[]).map((v) => v.Name);

async function addLibraryViaWizard(page: any, baseURL: string, name: string) {
  const path = await extraMediaPath(baseURL);
  await page.goto("/web/#/dashboard/libraries");
  await waitIdle(page);
  await page.getByRole("button", { name: "Add Media Library" }).click();
  // identified by content: the folder picker opens a second .dialog on top of it
  const dlg = page.locator(".dialog").filter({ has: page.locator("#selectCollectionType") });
  await expect(dlg.getByText("Content type")).toBeVisible();
  await dlg.locator("#selectCollectionType").selectOption("tvshows");
  await dlg.locator("#txtValue").fill(name);
  await dlg.locator(".btnAddFolder").click();
  await page.locator("#txtDirectoryPickerPath").fill(path);
  await page.locator("#txtDirectoryPickerPath").locator("xpath=ancestor::form").locator("button[type=submit]").click();
  await expect(dlg.getByText(path, { exact: true })).toBeVisible();
  await dlg.locator("button.btnSubmit").click();
  await expect(libMenu(page, name)).toBeVisible();
  return path;
}

test("libraries page lists the fixture libraries", async ({ page, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await page.goto("/web/#/dashboard/libraries");
  await waitIdle(page);
  await expect(page.getByRole("button", { name: "Add Media Library" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Scan All Libraries" })).toBeVisible();
  await expect(page.locator("#mediaLibraryPage p").getByText("Movies", { exact: true }).first()).toBeVisible();
  await expect(page.locator("#mediaLibraryPage p").getByText("Shows", { exact: true }).first()).toBeVisible();
  checkTrace("libraries-list", info.project.metadata.target, trace, since);
});

test("add a library through the wizard with a media path", async ({ page, baseURL, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  const path = await addLibraryViaWizard(page, baseURL!, NAME);
  const vf = (await adminCall(baseURL!, "GET", "/Library/VirtualFolders")).json as { Name: string; CollectionType: string; Locations: string[] }[];
  const lib = vf.find((v) => v.Name === NAME);
  expect(lib?.CollectionType).toBe("tvshows");
  expect(lib?.Locations).toEqual([path]);
  checkTrace("libraries-add", info.project.metadata.target, trace, since);
});

test("edit library options and persist them", async ({ page, baseURL, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  await addLibraryViaWizard(page, baseURL!, NAME);
  const opts = async () => ((await adminCall(baseURL!, "GET", "/Library/VirtualFolders")).json as any[]).find((v) => v.Name === NAME).LibraryOptions;
  const before = (await opts()).EnableRealtimeMonitor as boolean;
  const since = trace.mark();
  await libMenu(page, NAME).click();
  await page.getByRole("menuitem", { name: "Manage library" }).click();
  const dlg = page.locator(".dialog").filter({ has: page.locator(".chkEnableRealtimeMonitor") });
  const box = dlg.locator(".chkEnableRealtimeMonitor");
  await expect(box).toBeVisible();
  expect(await box.isChecked()).toBe(before);
  await box.locator("xpath=..").click();
  await dlg.locator("button.btnSubmit").click();
  await expect.poll(async () => (await opts()).EnableRealtimeMonitor).toBe(!before);
  // reopen: the dialog shows the persisted value
  await waitIdle(page);
  await libMenu(page, NAME).click();
  await page.getByRole("menuitem", { name: "Manage library" }).click();
  expect(await page.locator(".dialog").last().locator(".chkEnableRealtimeMonitor").isChecked()).toBe(!before);
  await page.locator(".dialog").last().locator("button.btnCancel").click();
  checkTrace("libraries-edit", info.project.metadata.target, trace, since);
});

test("rename a library", async ({ page, baseURL, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  await addLibraryViaWizard(page, baseURL!, NAME);
  const since = trace.mark();
  await libMenu(page, NAME).click();
  await page.getByRole("menuitem", { name: "Rename" }).click();
  const dlg = page.getByRole("dialog", { name: "Rename" });
  await dlg.getByRole("textbox", { name: "New name" }).fill(RENAMED);
  await dlg.getByRole("button", { name: "Rename" }).click();
  await expect(libMenu(page, RENAMED)).toBeVisible();
  expect(await libraryNames(baseURL!)).toContain(RENAMED);
  expect(await libraryNames(baseURL!)).not.toContain(NAME);
  checkTrace("libraries-rename", info.project.metadata.target, trace, since);
});

test("remove a library from the list", async ({ page, baseURL, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  await addLibraryViaWizard(page, baseURL!, NAME);
  const since = trace.mark();
  await libMenu(page, NAME).click();
  await page.getByRole("menuitem", { name: "Remove" }).click();
  await page.getByRole("dialog", { name: "Remove Media Folder" }).getByRole("button", { name: "Delete" }).click();
  await expect(page.locator("#mediaLibraryPage p").getByText(NAME, { exact: true })).toHaveCount(0);
  expect(await libraryNames(baseURL!)).not.toContain(NAME);
  // the fixture libraries are untouched
  expect(await libraryNames(baseURL!)).toEqual(expect.arrayContaining(["Movies", "Shows"]));
  checkTrace("libraries-remove", info.project.metadata.target, trace, since);
});
