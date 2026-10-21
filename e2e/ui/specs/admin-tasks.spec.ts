import { test, expect, loginAsAdmin, waitIdle, checkTrace, adminCall } from "../lib/fixtures";

// Admin dashboard: scheduled tasks. Triggering is done through the UI; maintenance tasks used here
// have no lasting effect on the server state the other specs depend on.
const taskItem = (page: any, title: string) =>
  page.getByRole("listitem").filter({ has: page.getByRole("heading", { name: title, level: 3 }) });
const findTask = async (baseURL: string, name: string) =>
  ((await adminCall(baseURL, "GET", "/ScheduledTasks")).json as any[]).find((t) => t.Name === name);

test("scheduled tasks page lists the task categories", async ({ page, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await page.goto("/web/#/dashboard/tasks");
  await waitIdle(page);
  for (const h of ["Library", "Maintenance"]) await expect(page.getByRole("heading", { name: h, level: 2 })).toBeVisible();
  for (const t of ["Scan Media Library", "Clean Cache Directory", "Clean Log Directory"]) {
    await expect(taskItem(page, t)).toBeVisible();
  }
  checkTrace("tasks-list", info.project.metadata.target, trace, since);
});

test("trigger a scheduled task from the UI", async ({ page, baseURL, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  await page.goto("/web/#/dashboard/tasks");
  await waitIdle(page);
  const before = await findTask(baseURL!, "Clean Log Directory");
  const since = trace.mark();
  await taskItem(page, "Clean Log Directory").getByRole("button").click();
  await expect.poll(async () => (await findTask(baseURL!, "Clean Log Directory")).LastExecutionResult?.EndTimeUtc, { timeout: 20_000 })
    .not.toBe(before.LastExecutionResult?.EndTimeUtc);
  const after = await findTask(baseURL!, "Clean Log Directory");
  expect(after.State).toBe("Idle");
  expect(after.LastExecutionResult.Status).toBe("Completed");
  await expect(taskItem(page, "Clean Log Directory")).toContainText("Last ran");
  checkTrace("tasks-trigger", info.project.metadata.target, trace, since);
});

test("stop a running scheduled task", async ({ page, baseURL, apiTrace: trace }, info) => {
  // A scan of the big fixture folders takes long enough to be stopped. None of them may overlap the
  // Movies/Shows library folders (deleting a library that shares files with another one removes
  // those items from the other library), and the temporary library is removed afterwards.
  const vf = (await adminCall(baseURL!, "GET", "/Library/VirtualFolders")).json as { Name: string; Locations: string[] }[];
  const root = vf.find((v) => v.Name === "Movies")!.Locations[0].replace(/\/Movies\/?$/, "");
  const LIB = "UIT Lib Stop";
  const dirs = ["Many", "Matrix2", "Art", "Extras", "Ignore", "SubFiles", "Translit", "Subs3", "Sorting", "Subs2", "EpNum", "ArtShows"];
  const counts = async () => JSON.stringify((await adminCall(baseURL!, "GET", "/Items/Counts")).json);
  const countsBefore = await counts();
  await adminCall(baseURL!, "DELETE", `/Library/VirtualFolders?name=${encodeURIComponent(LIB)}&refreshLibrary=true`);
  try {
    expect((await adminCall(baseURL!, "POST", `/Library/VirtualFolders?name=${encodeURIComponent(LIB)}&collectionType=movies&refreshLibrary=false`, { LibraryOptions: { PathInfos: dirs.map((d) => ({ Path: `${root}/${d}` })) } })).status).toBe(204);
    await loginAsAdmin(page);
    await page.goto("/web/#/dashboard/tasks");
    await waitIdle(page);
    const since = trace.mark();
    const item = taskItem(page, "Scan Media Library");
    await item.getByRole("button").click();
    await expect(item).toContainText("%");
    await expect.poll(async () => (await findTask(baseURL!, "Scan Media Library")).State).toBe("Running");
    await item.getByRole("button").click(); // the same button now stops the task
    await expect.poll(async () => (await findTask(baseURL!, "Scan Media Library")).State, { timeout: 30_000 }).toBe("Idle");
    await expect(item).not.toContainText("%");
    await adminCall(baseURL!, "DELETE", `/Library/VirtualFolders?name=${encodeURIComponent(LIB)}&refreshLibrary=true`);
    // removing the library with refresh drops its items again (asynchronously)
    await expect.poll(counts, { timeout: 60_000, message: "the fixture libraries must be untouched" }).toBe(countsBefore);
    checkTrace("tasks-stop", info.project.metadata.target, trace, since);
  } finally {
    await adminCall(baseURL!, "DELETE", `/Library/VirtualFolders?name=${encodeURIComponent(LIB)}&refreshLibrary=true`);
  }
});
