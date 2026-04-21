import { test, expect } from '@playwright/test';

async function waitForPreviewText(page, text: string) {
  const frame = page.frameLocator('#preview');
  await expect(frame.locator('body')).toContainText(text);
}

test.beforeEach(async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('#editor')).toBeVisible();
});

test('updates preview when editing markdown', async ({ page }) => {
  const editor = page.locator('#editor');
  await editor.fill('# Hello Karte\n\n**Preview** works.');
  await waitForPreviewText(page, 'Hello Karte');
  const heading = page.frameLocator('#preview').locator('h1');
  await expect(heading).toHaveText('Hello Karte');
  await expect(page.frameLocator('#preview').locator('strong')).toHaveText('Preview');
});

test('keeps the edited section visible in preview after typing near the end', async ({ page }) => {
  const editor = page.locator('#editor');
  const longContent = Array.from({ length: 40 }, (_, i) => `## Section ${i + 1}\n\nLine for section ${i + 1}.`).join('\n\n');
  await editor.fill(longContent);
  await waitForPreviewText(page, 'Section 40');

  await editor.press('End');
  await editor.type('\n\n## Tail Section\n\nTail content is here.');
  await waitForPreviewText(page, 'Tail Section');

  const scrollTop = await page.locator('#preview').evaluate((iframe: HTMLIFrameElement) => {
    const doc = iframe.contentDocument;
    const scrollRoot = doc?.scrollingElement || doc?.documentElement || doc?.body;
    return scrollRoot?.scrollTop ?? 0;
  });

  expect(scrollTop).toBeGreaterThan(0);
  await expect(page.frameLocator('#preview').locator('h2').last()).toHaveText('Tail Section');
});

test('renders CSV imports in preview', async ({ page }) => {
  const editor = page.locator('#editor');
  await editor.fill('CSV data below:\n\n@import data/sample.csv');

  const table = page.frameLocator('#preview').locator('table');
  await expect(table).toBeVisible();
  const rows = table.locator('tbody tr');
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText('AliceEngineerTokyo');
});

test('saves edited file successfully', async ({ page }) => {
  const editor = page.locator('#editor');
  await editor.fill('# Save Test');
  await expect(page.locator('#saveBtn')).toHaveClass(/unsaved/);
  await page.click('#saveBtn');
  await expect(page.locator('#saveBtn')).not.toHaveClass(/unsaved/);
});

test.fixme('keeps an inserted image visible after dropping onto the preview', async ({ page }) => {
  const editor = page.locator('#editor');
  const longContent = Array.from({ length: 35 }, (_, i) => `## Block ${i + 1}\n\nParagraph ${i + 1}.`).join('\n\n');
  await editor.fill(longContent);
  await waitForPreviewText(page, 'Block 35');

  await page.locator('#preview').evaluate((iframe: HTMLIFrameElement) => {
    const doc = iframe.contentDocument;
    if (!doc) return;
    const scrollRoot = doc.scrollingElement || doc.documentElement || doc.body;
    if (scrollRoot) {
      scrollRoot.scrollTop = scrollRoot.scrollHeight;
    }
  });
  await page.waitForTimeout(200);
  await page.locator('.image-thumbnail[data-image-path="data/image/mock-1.png"]').dragTo(page.locator('#preview'));

  await expect(editor).toHaveValue(/!\[mock-1\]\(data\/image\/mock-1\.png "mock-1\.png"\)/);
  await page.waitForFunction(() => {
    const iframe = document.getElementById('preview') as HTMLIFrameElement | null;
    const doc = iframe?.contentDocument;
    return !!doc?.querySelector('img[src="data/image/mock-1.png"]');
  });

  const result = await page.locator('#preview').evaluate((iframe: HTMLIFrameElement) => {
    const doc = iframe.contentDocument;
    const scrollRoot = doc?.scrollingElement || doc?.documentElement || doc?.body;
    const img = doc?.querySelector('img[src="data/image/mock-1.png"]') as HTMLElement | null;
    const viewportHeight = iframe.clientHeight || doc?.defaultView?.innerHeight || 0;
    if (!scrollRoot || !img) {
      return { scrollTop: 0, visible: false };
    }
    const rect = img.getBoundingClientRect();
    return {
      scrollTop: scrollRoot.scrollTop,
      visible: rect.top < viewportHeight && rect.bottom > 0,
    };
  });

  expect(result.scrollTop).toBeGreaterThan(0);
  expect(result.visible).toBe(true);
});

test.fixme('keeps an inserted csv preview visible after dropping onto the preview', async ({ page }) => {
  const editor = page.locator('#editor');
  const longContent = Array.from({ length: 35 }, (_, i) => `## Data ${i + 1}\n\nRow ${i + 1}.`).join('\n\n');
  await editor.fill(longContent);
  await waitForPreviewText(page, 'Data 35');

  await page.locator('#preview').evaluate((iframe: HTMLIFrameElement) => {
    const doc = iframe.contentDocument;
    if (!doc) return;
    const scrollRoot = doc.scrollingElement || doc.documentElement || doc.body;
    if (scrollRoot) {
      scrollRoot.scrollTop = scrollRoot.scrollHeight;
    }
  });
  await page.waitForTimeout(200);
  await page.locator('.csv-item[data-csv-path="data/sample.csv"]').dragTo(page.locator('#preview'));

  await expect(editor).toHaveValue(/@import\(type="csv", path="data\/sample\.csv"\)/);
  await expect(page.frameLocator('#preview').locator('table').last()).toBeVisible();

  const result = await page.locator('#preview').evaluate((iframe: HTMLIFrameElement) => {
    const doc = iframe.contentDocument;
    const scrollRoot = doc?.scrollingElement || doc?.documentElement || doc?.body;
    const table = doc?.querySelector('table:last-of-type') as HTMLElement | null;
    const viewportHeight = iframe.clientHeight || doc?.defaultView?.innerHeight || 0;
    if (!scrollRoot || !table) {
      return { scrollTop: 0, visible: false };
    }
    const rect = table.getBoundingClientRect();
    return {
      scrollTop: scrollRoot.scrollTop,
      visible: rect.top < viewportHeight && rect.bottom > 0,
    };
  });

  expect(result.scrollTop).toBeGreaterThan(0);
  expect(result.visible).toBe(true);
});
