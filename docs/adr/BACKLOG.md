# Backlog

Work this corpus has deliberately punted, each entry naming the record that
punted it. An entry here is written in the same commit as the deferral that
points at it — a pointer to a file that never received anything passes every
check there is and carries no work.

Nothing here is scheduled. This is the list a future `adr-write` session reads so
that a deferral resurfaces instead of quietly becoming permanent.

---

## The gaps that matter

These are not the same kind of thing as the rest of this file. Each one is a gap
in how this repository knows whether it works.

### Every check runs on every push

**Deferred by:** ADR-013 (run every test against the real migrations)

The repository has **no CI workflow**. `gofmt -l`, `go vet ./...`,
`go test ./...` and `scripts/smoke.sh` all exist, all pass, and all run only when
a person on a laptop chooses to run them.

GitHub's default-setup CodeQL *does* run on this repository and reports success.
That is worse than silence: it puts a green tick against a commit whose actual
checks nobody ran, and a green tick is read as "checked".

**What would make this real:** it already is. This is the largest open gap in the
repository, and the only reason it is deferred rather than done is that nobody
has asked for it.

### Nobody has opened the application in a browser

**Deferred by:** ADR-008 (no forms except the photo upload), ADR-009 (one stream per page), ADR-015 (an upload says it is uploading)

Six defects were found by a person using the application after the server-side
suite was green, and every one was invisible to that suite **by construction**:
an upload that never fired, photographs that could not be opened on a phone, a
saved batch with no link to it, places that could not be edited. A server-side
suite cannot see reachability or a client-side contract.

The markup contract test added afterwards
(`internal/web/view/upload_contract_test.go`) closes the specific hole it was
written for and nothing wider. Nothing checks that datastar hydrates at all, that
a patch is applied, that the layout survives a phone-sized viewport, or that
focus and contrast are usable.

**What would make this real:** walking intake → price → photo → cart → export on
a phone-sized viewport, and fixing what only a person can see. Then a
browser-driven test for the paths that matter.

### Down migrations are never exercised

**Deferred by:** ADR-013 (run every test against the real migrations)

Every migration has a `-- +goose Down` section and none of them has ever been
run. `TestMigrateIsIdempotent` runs the set forward twice; nothing runs it
backward. A down migration that does not work is discovered during the incident
it was written for.

### Four packages still build their own test schema

**Deferred by:** ADR-013 (run every test against the real migrations)

`internal/fx` (1 copied `CREATE TABLE`), `internal/location` (2),
`internal/cart` (5) and `internal/auth` (1) never run the migrations — they
build a schema of their own, each with a comment explaining why the copy is
acceptable. That is the same comment `internal/offer` and `internal/submission`
carried before their copies drifted and left the suite green against a schema
production does not have.

⚠ ADR-013 claimed "No package holds a copy of any schema statement" from
2026-09-06 until 2026-09-07, when this was found while reading `internal/fx` for
an unrelated reason. The claim was false when written; the record now says so.
Its `Enforced-by` test lives in `internal/offer`, so it proves that one package
runs the migrations and is structurally blind to these four — a record can be
mutation-verified and still overstate its reach.

**What would make this real:** nine statements, four helpers. Each conversion may
surface drift that has already happened, which is the point of doing it.

---

## Storage and money

### Automatic retry of a busied transaction

**Deferred by:** ADR-001 (open the database twice)

`_txlock=immediate` removes the upgrade conflict, so the only remaining
`SQLITE_BUSY` is an ordinary lock wait that exceeded `busy_timeout` — a
five-second queue. Today that surfaces to the user as a failed request. A retry
wrapper would absorb it, but a read-then-write retried from the top must re-read
the state it decided on, so the retry has to be *correct* per call site rather
than merely present. Not worth building until a five-second writer queue is
actually observed.

### A per-currency rounding policy for display

**Deferred by:** ADR-002 (money is integer minor units)

Rendering assumes an exponent of 2 is the common case and pads accordingly. A
currency with a different exponent renders correctly but has never been exercised
against real data, because nothing in this warehouse is priced in one.

## Prices and reporting

### A per-holder statement or payout run

**Deferred by:** ADR-003 (the owner price never leaves the building)

The data to answer "what do we owe Jonas this month" exists — owner price, sold
price, sale date, custodian resolved through the location tree — and nothing
assembles it. It is the obvious next report and it is not built.

### Reminding somebody that a draft has been unpriced for a long time

**Deferred by:** ADR-004 (an unpriced draft is a normal state)

The research queue makes unpriced drafts visible; nothing escalates one that has
sat there for a month.

### Bulk pricing of a whole batch in one screen

**Deferred by:** ADR-004 (an unpriced draft is a normal state)

Research is done for twenty items at a sitting; pricing is done one item at a
time.

## Places and stock

### Moving stock between locations in bulk

**Deferred by:** ADR-005 (storage is one recursive tree)

Moving a shelf's worth of items is one item at a time. Re-addressing the *tree* is
already transactional and bulk; moving the *stock* is not.

## Accounts

### Password reset

**Deferred by:** ADR-006 (registration closes after the first account)

There is none. An administrator who forgets their password needs another
administrator, or the database. With no mail transport in this application, the
cheapest honest form is an administrator setting a colleague's password, which
already exists — the gap is self-service.

### Two-factor authentication

**Deferred by:** ADR-006 (registration closes after the first account)

Not present. The account set is small and known, which is why this is deferred
rather than urgent.

### Rate limiting sign-in attempts

**Deferred by:** ADR-006 (registration closes after the first account)

Authentication is deliberately expensive (a real bcrypt comparison runs even when
no account matches), which bounds the rate somewhat. Nothing counts attempts.

### Rate limiting the public photograph route

**Deferred by:** ADR-007 (a photo UUID is its public capability)

`/p/` is unauthenticated by necessity. Nothing bounds how often it is hit.

## Photographs

### Watermarking or resizing on the fly

**Deferred by:** ADR-007 (a photo UUID is its public capability)

Photographs are served as uploaded. A marketplace's own resizing is doing this
work today.

### Drag-and-drop or paste-to-upload

**Deferred by:** ADR-008 (no forms except the photo upload)

The upload is a file input inside the one permitted form. Anything richer has to
respect the same datastar form contract, which is exactly the thing that was got
wrong once already.

## Submissions

### Letting a submitter edit a submission after sending it

**Deferred by:** ADR-011 (a submission is not an offer)

Once sent, a submission is the administrator's to act on. Correcting a typo means
asking them.

### Notifying the submitter of a decision

**Deferred by:** ADR-011 (a submission is not an offer)

The decision is communicated by the phone call that produced it. There is no mail
or SMS transport in this application.

### `core.Photo.OfferID` holds a submission id for submission photos

**Deferred by:** ADR-011 (a submission is not an offer)

`core.Photo` has no `SubmissionID` field, so a submission's photograph carries the
submission id in `OfferID`. It works, it is tested, and **the field name is a
lie**. Renaming it to something aggregate-neutral touches both packages and the
templates. Recorded so the next reader meets it here rather than in the debugger.

## Search

### Ranking tuned by field weight

**Deferred by:** ADR-012 (search is an index the database maintains)

All indexed columns rank equally. A title match probably deserves to outrank a
description match; nothing measures whether it does.

### Full-text search over submissions

**Deferred by:** ADR-012 (search is an index the database maintains)

The inbox filters by status and submitter, not by text. The FTS machinery exists
and is not applied to `submissions`.

## Interface

### Showing and editing how many of a thing we hold

**Deferred by:** ADR-020 (intake is one click, and the camera is first)

`core.Offer.Quantity` has existed since the first migration. It is constrained by
the schema (`INTEGER NOT NULL DEFAULT 1 CHECK (quantity >= 0)`), refused when
negative by `Offer.Validate`, rewritten by `Repo.UpdateOffer`, read back by the
cart, and written by BOTH exporters — eBay's `*Quantity` and Shopify's `Variant
Inventory Qty`.

⚠ **`internal/web` never sets it.** Every offer therefore carries the database
default of 1, and a warehouse holding five of something tells a marketplace it
holds one. That is a wrong number leaving the building, not merely a missing
input, which is why it is recorded here rather than left implicit.

ADR-020 deferred it because it is not a flow decision: exposing a field that
already exists end to end is presentation, and carries no record for the same
reason the mobile drawer and the responsive listing carry none.

**What would make this real:** it is real now — this entry exists to say the
deferral was ADR-020's scope boundary, not a judgement that the number does not
matter.

### Deleting an abandoned draft in one gesture

**Deferred by:** ADR-020 (intake is one click, and the camera is first)

ADR-020 makes "New offer" write to the database on the click, so a press followed
by second thoughts leaves an untitled draft behind. ADR-019's "Needs describing"
queue is where those become visible, and an offer can already be deleted — but
only by opening it first.

A confirmation step before creating was considered and refused: it is exactly the
step ADR-020 exists to delete. Making the DISPOSAL cheap is the right answer if
the litter becomes a nuisance, and it belongs on the queue, beside the row.

**What would make this real:** somebody actually accumulating abandoned drafts
and saying so.

### Every busy indicator gets its own signal

**Deferred by:** ADR-015 (an upload says it is uploading)

Nine controls share the signal `_busy`, and datastar signals are global and
flattened — so any one of them going busy spins the spinner and disables the
control on all the others that happen to be on the same page. On the offer detail
page that is five controls reacting to one.

ADR-015 avoided widening it: the upload got its own `_uploading` rather than
joining `_busy`. The existing nine were left alone, because renaming a signal
touches every consumer of it and the payoff is cosmetic. It is written down here
so the next person to notice a spinner spinning for no reason finds the answer
rather than a mystery.

**What would make this real:** somebody being confused by it, or a page where two
slow actions can genuinely be in flight at once.

### Assigning a photograph group to a cataloguer, or locking one while it is described

**Deferred by:** ADR-019 (an undescribed draft is a normal state)

ADR-019 makes "photographed but not yet named" a queue two people share. It gives
that queue no ownership at all: anybody can open any group, and two cataloguers
who both open `WH0000042` will both describe it, with the second save winning
silently.

That is tolerable at the size this is built for — a handful of people who can see
each other. It stops being tolerable the moment the queue is worked by people in
different places, which is exactly the arrangement ADR-005's offsite warehouses
already permit.

**What would make this real:** two people actually colliding, or the queue being
worked by somebody the photographer cannot shout to.

### Bulk-describing several photograph groups in one screen

**Deferred by:** ADR-019 (an undescribed draft is a normal state)

Describing is done one offer at a time, through the offer editor. A cataloguer
working a shelf of forty items pays a page load per item to type two fields.

⚠ This is the same shape as "Bulk pricing of a whole batch in one screen"
(deferred by ADR-004), and for the same reason — the queues are the same
mechanism one field apart. If either is ever built, build the other with it
rather than inventing a second bulk-edit surface.

**What would make this real:** somebody describing a large intake and saying so.

### Splitting or merging a photograph group

**Deferred by:** ADR-019 (an undescribed draft is a normal state)

A photograph group is a draft offer, so "one thing" is decided when the shutter
is pressed. There is no way to move a picture from one offer to another, so a
photographer who shoots two objects into one group, or one object across two,
leaves the cataloguer with no fix but to delete and re-photograph.

`Service.RemovePhoto` and `AddPhoto` exist, so the pieces are there; what is
missing is a move that keeps the photo id, and the id is both the public URL and
the blob's name on disk (ADR-007), so a move must not mint a new one.

**What would make this real:** it happening. It will — a person photographing
quickly does not always know where one lot ends.

### Allegro's and Shopify's own category settings and resolution

**Deferred by:** ADR-016 (marketplace configuration is data), ADR-019 (an undescribed draft is a normal state)

ADR-016 builds the mechanism — per-profile defaults in `settings`, per-offer
overrides in `offer_categories` — and wires only eBay through it, because eBay is
the profile that produced the reported error. Shopify's product type and
Allegro's "Main category / Subcategory" pair are the same shape and are not
wired.

⚠ Allegro's is **not a number**: its bulk template asks for a main category and a
subcategory written as NAMES, checked against Allegro's own help page on
2026-09-07. The `offer_categories.category` column is TEXT so it can hold either,
but the UI and any validation will need to know the difference.

**What would make this real:** the Allegro profile landing (ADR-018), or somebody
wanting Shopify product types set per item.

### A category picker or taxonomy browser

**Deferred by:** ADR-016 (marketplace configuration is data)

Today a category is a string an operator types or pastes. eBay's taxonomy has
tens of thousands of numbers and Allegro's has its own tree, and nothing here
helps you find the right one or tells you when you have typed a wrong one —
ADR-016 records that validating against a live taxonomy needs an authenticated
API call this application does not make.

**What would make this real:** somebody mis-filing enough items that hunting
numbers by hand stops being tolerable.

### A shared marketplace field as a real column on an offer

**Deferred by:** ADR-021 (a category is a tree that carries fields)

Brand, GTIN/EAN, MPN and weight are asked for by every marketplace, not by one
kind of thing. Modelling them as custom fields means every operator re-creates
them on every tree they build, and an exporter can never rely on them being
there. The alternative is real columns on `offers`. ADR-021 does not decide it,
because the custom-field mechanism has to exist before the question is even
answerable.

**What would make this real:** the first export that needs a GTIN, or an
operator who has typed "Brand" into three separate categories.

### Filtering or searching the offers listing by a custom field's value

**Deferred by:** ADR-021 (a category is a tree that carries fields)

ADR-021 makes `number` a distinct field kind precisely so a mileage can one day
be range-filtered, and then filters nothing. The listing searches title,
description and reference through the index the database maintains (ADR-012),
and a custom value lives in `offer_field_values`, where that index cannot see
it.

**What would make this real:** enough stock under one category that "which
turbochargers are under 100k km" stops being a question you answer by reading.

### A field whose options depend on another field's answer

**Deferred by:** ADR-021 (a category is a tree that carries fields)

The recar.lt reference screenshots show a quality list whose entries change with
the A/B/C condition grade — a conditional taxonomy. ADR-021's fields are
independent of one another by design, because a dependency between two fields is
a rule that has to be authored somewhere, and nothing here is that somewhere.

**What would make this real:** an operator maintaining one option list in three
places because only part of it applies at a time.

### Renaming `Offer.Categories` to end the word collision

**Deferred by:** ADR-021 (a category is a tree that carries fields)

ADR-016's `Offer.Categories` is where to list an item on each marketplace;
ADR-021's `Offer.CategoryID` is what the item IS. Two different things wearing
one word, mitigated today only by doc comments that point at each other. The
rename is mechanical and reaches the exporter, the handlers and the templates,
so it is its own change rather than a rider on this one.

**What would make this real:** the first bug where somebody reads one and means
the other.

## Operations

### Barcode or QR generation for a reference

**Deferred by:** ADR-010 (references are allocated by the database)

`WH0000042` is designed to be written and read by hand, which was the
requirement. A scannable label is the natural next step and is not built.

### A settings audit log

**Deferred by:** ADR-014 (deployment values live in the database)

The `settings` table stores a value and an `updated_at`, and not who changed it
or what it was before. With one setting and a handful of administrators that is
tolerable.
