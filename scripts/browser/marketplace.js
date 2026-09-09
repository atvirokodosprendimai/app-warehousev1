// A real-browser walk of the lists a marketplace must-have is picked from
// (ADR-022 T3).
//
// ⚠ THIS IS THE ONLY CHECK THAT CAN SEE THE BINDING, and the binding is where
// this feature's one silent trap lives. scripts/smoke.sh posts the signal names
// as JSON directly, so it proves the HANDLER reads `optValueCategory` — it can
// never prove the INPUT sends it. The two are joined only in a browser:
//
//   1. The add-boxes carry a data-bind whose attribute NAME is built per field
//      through a templ ATTRIBUTE SPREAD, not a literal attribute. Nothing
//      outside a browser proves the spread produced an attribute datastar
//      picked up at all.
//   2. HTML LOWERCASES ATTRIBUTE NAMES. `data-bind:opt-value-category` binds the
//      camelCase signal `optValueCategory`; writing the attribute camelCase
//      binds `optvaluecategory` — a DIFFERENT signal — and nothing reports it.
//      The page still renders, the button still posts, and the handler reads an
//      empty string. So the row simply never appears, which is what this walk
//      is here to notice.
//   3. The card is patched back over SSE after every add. A patch can only
//      replace an element already in the document, so the card is rendered
//      unconditionally with a stable id — and only a browser can tell whether
//      the replacement actually landed.
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

  console.log('== the list editor is on the settings screen ==');
  await page.goto(BASE + '/settings', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(400);

  is('the card is in the document before anything is added',
    await page.locator('#marketplace-options').count(), 1);

  // Every field gets a section even with nothing on its list — a section that
  // appeared only once it had content would be one nobody could add the first
  // value to, which is the state every fresh installation is in.
  is('all three must-haves have a section',
    await page.locator('#marketplace-options h3').count(), 3);

  console.log('== typing into a box reaches the handler as the right signal ==');
  await page.fill('input[aria-label="eBay category value"]', '20081');
  await page.fill('input[aria-label="eBay category label"]', 'Antiques');

  // ★ THE ASSERTION THE WHOLE WALK EXISTS FOR. If the data-bind attribute named
  // a different signal, both boxes would still fill, the post would still fire,
  // and the handler would read "" — so AddOption refuses and no row appears.
  await page.click('#marketplace-options button:has-text("Add")');
  await page.waitForTimeout(700);

  const row = page.locator('#marketplace-options li:has-text("Antiques")');
  is('what was typed reaches the server and comes back as a row',
    await row.count(), 1);
  is('and the row shows the value the marketplace will receive',
    (await page.locator('#marketplace-options li code').first().innerText()).trim(), '20081');

  // The boxes must end up EMPTY: whatever was submitted is one press away from
  // being submitted again, and the second press is a duplicate the operator did
  // not mean. This is the signal patch landing, which nothing else can see.
  is('the boxes are emptied by the save, not left loaded',
    await page.inputValue('input[aria-label="eBay category value"]'), '');

  console.log('== a duplicate says so, in words ==');
  await page.fill('input[aria-label="eBay category value"]', '20081');
  await page.fill('input[aria-label="eBay category label"]', 'Antiques again');
  await page.click('#marketplace-options button:has-text("Add")');
  await page.waitForTimeout(700);

  const msg = await page.locator('#settings-msg').innerText().catch(() => '');
  if (/already on the list/i.test(msg)) {
    ok('the duplicate refusal is a sentence an operator can act on');
  } else {
    fail('the duplicate refusal is a sentence an operator can act on', `got ${JSON.stringify(msg)}`);
  }
  is('and it did not add a second row', await row.count(), 1);

  console.log('== removing takes it off the list ==');
  await page.click('#marketplace-options button[aria-label="Remove Antiques"]');
  await page.waitForTimeout(700);
  is('the row is gone', await row.count(), 0);
  is('and the section is back to its addable empty state',
    await page.locator('input[aria-label="eBay category value"]').count(), 1);

  await page.screenshot({ path: `${SHOTS}/marketplace-lists.png`, fullPage: true });

  // ── ADR-022 T4: the offer editor picks from those lists ──────────────────
  //
  // ⚠ A <select> THAT RENDERS PERFECTLY AND IS BOUND TO NOTHING LOOKS IDENTICAL
  // IN HTML. scripts/smoke.sh posts offerEbayCategory as JSON, so it proves the
  // handler stores what it is sent; only picking an option in a real browser
  // proves the control sends anything at all. This is the same trap as the
  // add-boxes above, one screen along.
  console.log('== the offer editor picks from the list ==');
  await page.fill('input[aria-label="eBay category value"]', '20081');
  await page.fill('input[aria-label="eBay category label"]', 'Antiques');
  await page.click('#marketplace-options button:has-text("Add")');
  await page.waitForTimeout(700);

  await page.goto(BASE + '/offers', { waitUntil: 'domcontentloaded' });
  await page.click('button:has-text("New offer")');
  await page.waitForURL(/\/offers\/[0-9a-f-]{36}$/, { timeout: 15000 });
  const offerURL = page.url();
  ok('an offer to put a category on');

  is('the must-have is a dropdown now, not a box',
    await page.locator('#offer-marketplace select#o-mkt-category').count(), 1);

  // The empty option has to say what it falls back to. "Use the default" alone
  // means opening Settings to find out what you just agreed to.
  const emptyLabel = await page.locator('#o-mkt-category option[value=""]').innerText();
  if (/Use the default/.test(emptyLabel)) {
    ok('and its empty option names the fallback');
  } else {
    fail('and its empty option names the fallback', `got ${JSON.stringify(emptyLabel)}`);
  }

  // ★ THE ASSERTION T4 EXISTS FOR.
  await page.selectOption('#o-mkt-category', '20081');
  await page.click('#offer-marketplace button:has-text("Save marketplace details")');
  await page.waitForTimeout(800);

  await page.goto(offerURL, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(400);
  is('picking a category in a browser stores it',
    await page.locator('#o-mkt-category').inputValue(), '20081');

  // Empty must stay reachable, or an offer filed by mistake could never be put
  // back to "whatever the default is" (ADR-004's rule, surviving the dropdown).
  await page.selectOption('#o-mkt-category', '');
  await page.click('#offer-marketplace button:has-text("Save marketplace details")');
  await page.waitForTimeout(800);
  await page.goto(offerURL, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(400);
  is('and choosing nothing again clears it',
    await page.locator('#o-mkt-category').inputValue(), '');

  await page.screenshot({ path: `${SHOTS}/marketplace-offer.png`, fullPage: true });
  await page.goto(BASE + '/settings', { waitUntil: 'domcontentloaded' });

  const over = await page.evaluate(() => ({
    scroll: document.documentElement.scrollWidth,
    client: document.documentElement.clientWidth,
  }));
  if (over.scroll > over.client) {
    fail('the list editor fits a 390px viewport', `scrollWidth ${over.scroll} > clientWidth ${over.client}`);
  } else {
    ok('the list editor fits a 390px viewport');
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
