# app-warehousev1

A small warehouse and listing tool. You photograph a thing, say what it is, put
it on a shelf, work out what it is worth, and export it to Shopify or eBay.

Go, [templ](https://templ.guide), [datastar](https://data-star.dev) over SSE, and
SQLite with no cgo. One binary, one database file, one directory of photos.

## What it does

- **Intake in the order the work actually happens.** Photograph, title, shelve —
  and price *later*, after you have found out what the thing can fetch. An
  unpriced draft is a normal state, not an incomplete row, and the ones waiting
  on that research show up as their own queue.
- **Two prices, because there are two.** The *shop price* is what gets published.
  The *owner price* is what the person whose warehouse it is sitting in wants to
  receive. The margin is the difference, and it is shown at the moment you are
  deciding. The owner price is never exported.
- **Where things live, including other people's places.** Storage is a tree:
  site → building → room → aisle → shelf → segment → bin. Every level is
  optional, so a tiny warehouse is one site holding bins directly and grows
  intermediate levels later without moving any stock. A site carries a custodian,
  a city and a country, and everything inside it inherits them — which is what
  makes a garage in another town, held by someone else, addressable.
- **Status you can act on.** Draft, listed, pending, sold, archived. Only listed
  and pending are exported; a draft or an archived item cannot reach a
  marketplace.
- **Sold price and sale date kept separately** from the asking price, so a report
  can show what was realised against what was asked and what was owed.
- **Photos with public URLs.** Each photo is a UUID, and that UUID is its public
  URL. Shopify and eBay fetch images from their own servers, so the URL has to be
  public, stable, and not guessable — a UUID is all three.
- **EUR is the base currency**, with the European Central Bank's daily reference
  rates pulled in so a sale in another currency can be reported in EUR at the
  rate that applied *on the day of the sale*.

## Running it

    go build ./cmd/warehouse
    ./warehouse

Then open http://localhost:8080. **The first account you create becomes the
administrator**, and that page closes permanently afterwards. There is no public
registration: every later account is created by an administrator.

| Variable | Default | What it is |
|---|---|---|
| `ADDR` | `:8080` | Listen address |
| `DB_PATH` | `data/warehouse.db` | SQLite file |
| `PHOTOS_DIR` | `data/photos` | Photo blobs |
| `PUBLIC_BASE_URL` | `http://localhost:8080` | The origin a marketplace fetches photos from |
| `SECURE_COOKIES` | `false` | Set true behind HTTPS |

⚠ `PUBLIC_BASE_URL` must be an absolute, publicly reachable URL before you
export. A marketplace that cannot fetch an image does not report an error — it
just produces a listing with no pictures.

## How it is put together

    cmd/warehouse      the binary: config, wiring, routes
    internal/core      domain types and ports. Depends on nothing.
    internal/store     the database handles and migrations
    internal/auth      accounts, sign-in, the first-admin bootstrap
    internal/offer     offers and photos
    internal/location  the storage tree
    internal/fx        ECB reference rates
    internal/export    Shopify and eBay CSV profiles
    internal/blob      photo bytes on disk
    internal/web       handlers, views, the design system
    migrations         goose SQL

Every package depends on `internal/core` and the standard library, and never on a
sibling. `core` holds the shared types *and the ports*, and the ports are where
the read/write split lives: a read model is handed a `Reader` and therefore
cannot write, because it was never given anything that can.

### The database is opened twice, deliberately

Not redundancy — correctness. SQLite grants a deferred transaction its write lock
on the first *write* statement, so a transaction that reads first must upgrade
its lock, and an upgrade conflict returns `SQLITE_BUSY` immediately without ever
consulting the busy handler. `busy_timeout` therefore does nothing for the
read-then-write shape that almost every service method has.

Measured on this driver, 8 goroutines × 40 read-then-write transactions:

| DSN | failed |
|---|---|
| deferred (the default) | 257 / 320 |
| `_txlock=immediate` | 0 / 320 |

So the writer takes its lock at `BEGIN` and is capped at one connection, and the
reader is a normal pool carrying `query_only(1)` — which means the driver itself
refuses a write on the read path. Capping the whole application at one connection
also removes the failure, but it serialises reads through that connection and
throws away the reader/writer concurrency WAL was enabled for.

`internal/store` has a test that asserts *both* arms of that table. If it ever
passed vacuously — because the driver stopped honouring the DSN parameter — the
deferred arm would stop failing and the test would say so.

### The frontend holds almost no state

The browser reports what happened; the server decides what the page looks like
and streams HTML fragments back, morphed in by id. There are **no `<form>`
elements** — inputs bind to datastar signals — with one exception, the photo
upload, because bytes cannot ride in a signal.

Two things worth knowing before editing a template:

- Attribute names are lowercased by HTML, so a bind is written kebab-case in the
  attribute and refers to a camelCase signal: `data-bind:shop-amount` binds
  `shopAmount`. Writing `data-bind:shopAmount` binds `shopamount` — a different
  signal — and nothing reports it.
- Signals are global and flattened, with no component scope, and every unprefixed
  signal is sent to the backend on *every* action. Keep them few, name them as if
  they were globals, and prefix a purely presentational one with `_` so it stays
  in the browser.

Run `templ generate` after editing a `.templ` file, and never edit a `*_templ.go`.

## Checks

    go test ./...

Templates must be regenerated first if a `.templ` changed.
