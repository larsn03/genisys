// Browser-level e2e against `genisys serve` + mock endpoint
// (IMPL §e2e-mock; SPEC M2 exit gate + P3-13 DoD).

import { expect, test } from "@playwright/test";

const TOKEN = process.env.GENISYS_TOKEN ?? "";
const open = (page: import("@playwright/test").Page) =>
  page.goto(`/?token=${TOKEN}`, { waitUntil: "domcontentloaded" });

test("chat round-trip, then reload replays from the ledger", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "+ New session" }).click();
  const box = page.getByPlaceholder(/Ask Genisys|Type to interject/);
  await expect(box).toBeVisible();
  await box.fill("hello there");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator(".md").last()).toContainText("All finished.", { timeout: 20_000 });

  // reload = fresh tab: messages come back from the persisted rows
  await open(page);
  await expect(page.locator(".md").last()).toContainText("All finished.", { timeout: 20_000 });
  // and the event ledger replay marks the session idle, not running
  await expect(page.getByText("working", { exact: true })).toHaveCount(0);
});

test("send-while-running: queue chip, promote to interrupt, clean stop", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "+ New session" }).click();
  const box = page.getByPlaceholder(/Ask Genisys|Type to interject/);
  await box.fill("please sleep a while");
  await page.getByRole("button", { name: "Send" }).click();

  // the shell tool starts running (step trace opens live)
  await expect(page.getByText("Working…")).toBeVisible({ timeout: 20_000 });

  // interject while running → Queue mode
  await box.fill("stop now");
  await page.getByRole("button", { name: "Queue" }).click();
  await expect(page.getByText("stop now")).toBeVisible();

  // promote the queued interjection to interrupt (P3-13 DoD)
  await page.getByTitle("Promote to interrupt (act now)").click();

  // interrupt confirmation card + killed tool + honored instruction
  await expect(page.getByText("Interrupting — stopping the running action")).toBeVisible({
    timeout: 15_000,
  });
  // expand the step trace and confirm the tool was closed as interrupted
  await page.getByText(/Worked/).first().click();
  await expect(page.getByText("[interrupted", { exact: false }).first()).toBeVisible({
    timeout: 15_000,
  });
  await expect(page.locator(".md").last()).toContainText("All finished.", { timeout: 20_000 });
});

test("attachments upload into the scratchpad", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "+ New session" }).click();
  await page
    .locator('input[type=file]')
    .setInputFiles({ name: "note.txt", mimeType: "text/plain", buffer: Buffer.from("attached!") });
  await expect(page.getByText("📎 note.txt")).toBeVisible();
  await page.getByPlaceholder(/Ask Genisys/).fill("read the attachment");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator(".md").last()).toContainText("All finished.", { timeout: 20_000 });
  // the user bubble shows the attachment path
  await expect(page.getByText("[attached files — read them with read_file_lines]")).toBeVisible();
});
