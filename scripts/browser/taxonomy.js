// A real-browser walk of the operator's own taxonomy (ADR-021) and the
// questions it makes an offer answer.
//
// The server-side suite and scripts/smoke.sh both drive HTTP directly, so
// neither can see whether datastar actually BINDS these controls — and the
// binding is where this feature's traps live. Three in particular are invisible
// to every check that came before:
//
//   1. The per-field controls bind a signal named for the field's UUID through a
//      templ ATTRIBUTE SPREAD, not a literal attribute. Nothing outside a browser
//      proves the spread produced a real data-bind that datastar picked up.
//   2. A yes/no question is a CHECKBOX bound to a JS boolean. The smoke walk
//      answers text fields only, so the bool round-trip — seed false, tick, save,
//      still ticked — is exercised nowhere else.
//   3. The questions card ARRIVES OVER SSE when a category is chosen. A patch can
//      only replace an element already in the document, and nothing server-side
//      can tell whether it landed.
const { chromium } = require('playwright');

const BASE = process.env.BASE || 'http://127.0.0.1:18098';
const SHOTS = process.env.SHOTS || '.';
const CHROME = process.env.CHROME;

let failures = 0;
const ok = (name) => console.log(`  ok   ${name}`);
const fail = (name, detail) => { failures++; console.log(`  FAIL ${name}${detail ? ' — ' + detail : ''}`); };
const is = (name, actual, expected) =>
  actual === expected ? ok(name) : fail(name, `expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);

(async () => {
  const browser = await chromium.launch({ executablePath: CHROME });
  const ctx = await browser.newContext({
    viewport: { width: 390, height: 844 },
    deviceScaleFactor: 2,
    isMobile: true,
    hasTouch: true,
  });
  const page = await ctx.newPage();

  const consoleErrors = [];
  page.on('console', (m) => { if (m.type() === 'error') consoleErrors.push(m.text()); });
  page.on('pageerror', (e) => consoleErrors.push('pageerror: ' + e.message));

  console.log('== bootstrap ==');
  await page.goto(BASE + '/bootstrap', { waitUntil: 'domcontentloaded' });
  await page.fill('#name', 'M');
  await page.fill('#email', 'm@example.com');
  await page.fill('#password', 'correct-horse-battery');
  await Promise.all([
    page.waitForURL(BASE + '/', { timeout: 15000 }).catch(() => {}),
    page.click('button[type="submit"], button:has-text("Create")'),
  ]);
  ok('signed in as the first administrator');

  console.log('== the taxonomy screen is REACHABLE, not just addressable ==');
  // The nav lives in the off-canvas drawer on a phone, so it has to be opened
  // first — which is itself the check: a link nobody can reach is not a link.
  await page.click('.nav-toggle, [aria-controls="sidebar"], button:has-text("Menu")').catch(() => {});
  await page.waitForTimeout(300);
  const catLink = await page.locator('a[href="/categories"]').count();
  is('the drawer carries a Categories entry', catLink > 0, true);

  await page.goto(BASE + '/categories', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(400);

  console.log('== a starter template builds a trade in one press ==');
  // ⚠ ON THE EMPTY SCREEN, which is the state a template is for and the one in
  // which a control tucked beside an existing tree would not render at all.
  is('the empty screen offers the templates',
    await page.locator('button:has-text("PC parts")').count(), 1);

  await page.click('button:has-text("PC parts")');
  // The screen repaints over SSE — no reload. If the patch never lands the tree
  // stays empty and this waits out rather than passing on a page that only looks
  // right after F5.
  let built = true;
  await page.waitForSelector('.tree-row', { timeout: 8000 }).catch(() => { built = false; });
  is('one press builds the tree, with no reload', built, true);
  if (built) {
    const tree = await page.textContent('.tree');
    is('the root is there', /PC/.test(tree || ''), true);
    is('and its subcategories with it', /PC\/GPU/.test(tree || ''), true);
    is('all six of them', (tree.match(/PC\//g) || []).length, 6);
  }

  console.log('== creating a category through the UI (the datastar binding check) ==');
  await page.fill('#n-code', 'CAR');
  await page.fill('#n-name', 'Car parts');
  await Promise.all([
    page.waitForURL(/\/categories\?at=/, { timeout: 15000 }).catch(() => {}),
    page.click('button:has-text("Add category")'),
  ]);
  const onNode = /\/categories\?at=/.test(page.url());
  is('the form created a category and landed on it', onNode, true);
  if (!onNode) {
    console.log('       url: ' + page.url());
  }

  console.log('== a yes/no question, which nothing else round-trips ==');
  await page.fill('#f-label', 'Original part');
  await page.fill('#f-code', 'oem');
  await page.selectOption('#f-kind', 'bool');
  // The export tick, which is the thing M asked for.
  await page.check('#f-export');
  await Promise.all([
    page.waitForURL(/\/categories\?at=/, { timeout: 15000 }).catch(() => {}),
    page.click('button:has-text("Add question")'),
  ]);
  await page.waitForTimeout(400);
  const body = await page.textContent('body');
  is('the question was created', /Original part/.test(body), true);
  is('and it is marked as exported in the list', /exported/.test(body), true);

  const catURL = page.url();

  console.log('== an offer is asked it, LIVE, and the checkbox round-trips ==');
  await page.goto(BASE + '/offers', { waitUntil: 'domcontentloaded' });
  await Promise.all([
    page.waitForURL(/\/offers\/[0-9a-f-]{36}$/, { timeout: 15000 }).catch(() => {}),
    page.click('button:has-text("New offer")'),
  ]);
  const offerURL = page.url();
  is('New offer created a draft and landed on it', /\/offers\/[0-9a-f-]{36}$/.test(offerURL), true);

  // ⚠ THE PATCH TARGET. A fresh draft has no category and so has nothing to be
  // asked — but the wrapper has to be in the document anyway, because an SSE
  // patch can only replace an element that is already there.
  is('an unfiled draft still carries the questions wrapper',
    await page.locator('#offer-fields').count(), 1);
  is('and is asked nothing yet',
    await page.locator('input[type="checkbox"][id^="cf-"]').count(), 0);

  // File it under the CAR node this walk made. ⚠ BY VALUE, using the id out of
  // the URL it landed on: an index would be guesswork now that the PC template
  // has put seven other categories in the list, and `selectOption` does NOT
  // accept a regular expression for `label` — it takes a literal string, so a
  // regex there silently matches nothing.
  const carID = (catURL.match(/at=([0-9a-f-]{36})/) || [])[1];
  is('the walk knows which category it just made', typeof carID, 'string');
  await page.selectOption('#o-category', { value: carID });
  const picked = await page.locator('#o-category option:checked').textContent();
  is('the picker offers the tree by path', /CAR/.test(picked || ''), true);

  // ⚠ NO SAVE, NO RELOAD — THIS IS THE DEFECT M REPORTED ON 2026-09-07. Choosing
  // a category used to write a signal and nothing else, so the questions turned
  // up only on the next full page load. This walk used to click "Save details"
  // and then call page.reload(), which is precisely what hid it: the reload is
  // the one step an operator filing a shelf of parts never takes.
  let appeared = true;
  await page.waitForSelector('input[type="checkbox"][id^="cf-"]', { timeout: 8000 })
    .catch(() => { appeared = false; });
  is('choosing a category asks its questions, with no save and no reload', appeared, true);

  if (appeared) {
    const cb = page.locator('input[type="checkbox"][id^="cf-"]').first();
    // ⚠ THE SEED. An unanswered bool must arrive UNTICKED. A checkbox bound to
    // the string "" or "false" renders TICKED, because a non-empty string is
    // truthy and "" coerces — which is why the seed writes a real JS boolean.
    // The card that just appeared was seeded over the wire, not by the page.
    is('an unanswered yes/no starts unticked', await cb.isChecked(), false);

    await cb.check();
    await page.click('button:has-text("Save these details")');
    await page.waitForTimeout(900);

    // ⚠ STILL NO RELOAD. Saving broadcasts, and the stream patches this very
    // card back over the top of itself. A patch arriving without its signals
    // would blank the tick here, and that reads as a save that was lost.
    is('the tick survives the save’s own re-render', await cb.isChecked(), true);

    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(400);
    const after = page.locator('input[type="checkbox"][id^="cf-"]').first();
    is('and survives a reload, so it really was stored', await after.isChecked(), true);
  }

  console.log('== the screens do not overflow a 390px phone ==');
  for (const [name, url] of [['the offer editor', offerURL], ['the taxonomy screen', catURL]]) {
    await page.goto(url, { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(300);
    const over = await page.evaluate(() => ({
      scroll: document.documentElement.scrollWidth,
      client: document.documentElement.clientWidth,
    }));
    if (over.scroll > over.client) {
      fail(`${name} fits a 390px viewport`, `scrollWidth ${over.scroll} > clientWidth ${over.client}`);
    } else {
      ok(`${name} fits a 390px viewport`);
    }
    await page.screenshot({ path: `${SHOTS}/${name.replace(/\s+/g, '-')}.png`, fullPage: true });
  }

  const real = consoleErrors.filter((t) => !/favicon/i.test(t) && !/404/.test(t));
  if (real.length) {
    fail('the browser console is clean', real.slice(0, 5).join(' | '));
  } else {
    ok('the browser console is clean (apart from the pre-existing favicon 404)');
  }

  await browser.close();
  console.log(`\nBROWSER_FAILURES=${failures}`);
  process.exit(failures ? 1 : 0);
})().catch((e) => { console.error('HARNESS ERROR: ' + e.message); process.exit(2); });
