# ADR-002: Store money as integer minor units carrying its own currency

**Status:** Accepted
**Date:** 2026-09-06
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-003, `internal/core/money.go`
**Governs:** `internal/core/money.go`, `internal/core/fx.go`
**Enforced-by:** `internal/core/money_test.go::TestParseMoneyRoundTrips`
**Invalidates:** none — checked
**Served-path change:** A price typed as `12,50` or `12.50` is stored, redisplayed and exported as exactly `12.50`; no arithmetic in the application can introduce a rounding error a person would see.

## Context

This record is retrospective; the representation was chosen when the domain was
first written and is recorded now because changing it later means a data
migration over every price column in the database.

Three prices per offer, two currencies in play from the start, and a CSV export
whose whole purpose is to be read by somebody else's importer. A float would
make every one of those a place where a half-cent can appear, and the failure is
silent: the number looks right until it is summed.

Currency is carried *with* the amount rather than beside it because minor units
are only meaningful within one currency — `1250` is €12.50 or ¥1250 depending on
an exponent that is a property of the currency, not of the column.

## Existing Primitives Audit

- Go's `int64` — **reused** as the storage type. Nothing is built on top of it
  beyond a struct and an exponent table.
- `strconv` / `fmt` — **reused** for parsing and rendering. No decimal library is
  pulled in: the operations this application performs are addition, subtraction
  and one multiplication by a rate, which integers do exactly.
- No prior money type existed to reshape.

## Decision

`core.Money` is `{Minor int64, Currency string}`. The amount is always in the
currency's smallest unit, and the exponent that relates them is looked up from
the currency rather than assumed to be 2.

A zero `Money` means "not set", not "free" — which is why `IsZero` exists and why
`Validate` skips exponent checks on a zero-currency amount. That distinction is
load-bearing: an unpriced draft (ADR-004) and an item priced at nothing must not
be the same value.

Parsing accepts both `,` and `.` as the decimal separator, because the operator
types on a Lithuanian keyboard and a comma is what comes out. Excess precision is
refused rather than rounded: silently turning `12.999` into `13.00` is a decision
the application is not entitled to make on somebody's behalf.

What would FAIL this decision: `TestParseMoneyRoundTrips` failing to return the
input string, or `TestMarginRefusesToSubtractAcrossCurrencies` passing when it
should refuse. Both run on every `go test ./...` against generated inputs, so the
data that could produce the failure exists on every run.

## Alternatives Considered

- **`float64`.** Rejected: `0.1 + 0.2` is the standard counter-example and a
  price feed is exactly where it is unacceptable. The failure is silent and
  appears in a total nobody can reproduce.
- **A decimal library (`shopspring/decimal` or similar).** Rejected as
  disproportionate: it adds a dependency and an allocation per arithmetic
  operation to buy arbitrary precision this application never needs. Integers are
  exact for the four operations performed.
- **Minor units with the currency held on the offer, not the amount.** Rejected
  because three amounts on one offer may legitimately differ in currency — an
  item can be listed in EUR and owed to its holder in USD — and one currency
  field would force a conversion at write time, at a rate nobody recorded.
- **A string column holding the formatted decimal.** Rejected: it moves the
  arithmetic into the reader, and comparison (a price-range filter) becomes
  lexical rather than numeric.

## Component / Boundary Impact

`internal/core` owns `Money`, `ParseMoney`, `Decimal` and the exponent table.
Every other package uses it and none re-implements it. Storage is two columns per
price (`*_minor INTEGER`, `*_currency TEXT`), so the representation is visible in
the schema and cannot be quietly widened by an ORM. One reason to change: how
this application represents an amount of money.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `core.Money{Minor, Currency}` | new type | `internal/core` | offer, submission, export, fx, web |
| `offers.shop_minor` / `shop_currency` and the owner/sold pairs | new columns, integer + text | `migrations/00001_init.sql` | `internal/offer` |
| `core.ParseMoney(string, currency)` | new; accepts `,` and `.` | `internal/core` | every handler that reads a typed price |

## Inter-task Contracts

None.

## Implementation

Already implemented, on `main`, in `internal/core/money.go` and the `*_minor` /
`*_currency` column pairs of `migrations/00001_init.sql`. No tasks directory:
this is a retrospective record.

## Consequences

- **Positive:** no rounding error is reachable by any arithmetic this application
  performs.
- **Positive:** a price-range filter compares integers, and the index on
  `(shop_currency, shop_minor)` leads with the currency so the comparison is only
  ever made within one.
- **Negative:** every read and write goes through a conversion, and a developer
  who writes `Minor: 12` meaning twelve euros is wrong by a factor of a hundred
  with nothing to catch it.
- **Neutral:** currencies with an exponent other than 2 work, but no test data
  exercises one in anger.

## Out of Scope

- Arbitrary-precision arithmetic (permanent: boundary: this application adds, subtracts and multiplies by one rate; integers are exact for all three)
- Automatic currency conversion inside an arithmetic operation (permanent: boundary: a conversion needs a dated rate the caller chose — see ADR-003 and `core.Rate`)
- Currencies whose minor unit is not a power of ten (permanent: fact: no ISO 4217 currency in use has a non-decimal minor unit; the historical ones were withdrawn; citation: url https://www.iso.org/iso-4217-currency-codes.html)
- A per-currency rounding policy for display (deferred: docs/adr/BACKLOG.md)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Someone constructs `Money{Minor: 12}` meaning €12 | Med | Med | `ParseMoney` is the only documented constructor; `Decimal` renders so a wrong value is visible immediately |
| A currency with exponent ≠ 2 is added and rendering is wrong | Low | Med | `TestDecimalPadsSubUnitAmounts` covers the padding path; `Exponent` returns an error for an unknown code |
| A zero amount is read as "free" rather than "unset" | Med | High | `IsZero` is used everywhere the distinction matters, and `NeedsPricing` depends on it |

## Rollback

Reversible only by migration: the columns would have to be rewritten to a
different type and every stored amount converted. There is no in-place rollback,
which is precisely why the choice is recorded rather than left implicit.

## Follow-ups

None — nothing was left open when this record was written.
