# MarketHouse Backend — README

## Stack

| Layer | Tech |
|---|---|
| Language | Go 1.22+ |
| Framework | Gin |
| Database | PostgreSQL |
| Cache / OTP | Redis |
| File storage | Local (`./uploads/`) — swap to S3 via `Storage` interface |
| Auth | JWT (access + refresh) |
| Payment | Mock by default → swap to Paystack or Flutterwave |

---

## Quick Start

```bash
# 1. Clone and enter
git clone ... && cd markethouse

# 2. Copy env
cp .env.example .env        # fill in values

# 3. Run SQL (idempotent — safe to re-run; also applied automatically on boot
#    to any database that doesn't have the base schema yet)
psql -U postgres -d markethouse -f internal/database/vinci.sql

# 4. Start
go run ./cmd/server/main.go
```

### `.env` variables

```env
PORT=8080
DB_DSN=postgres://user:pass@localhost:5432/markethouse?sslmode=disable
REDIS_ADDR=localhost:6379
JWT_SECRET=change_me_in_production
REFRESH_SECRET=also_change_this

# Payment — leave as "mock" for testing, switch to "paystack" for live
PAYMENT_PROVIDER=mock
PAYSTACK_SECRET_KEY=sk_test_...
FLW_SECRET_KEY=FLWSECK_TEST-...
```

---

## Authentication Flow

```
POST /signup       → creates user (account_type + business_type stored)
POST /verify       → confirms OTP → returns access_token + refresh_token
POST /login        → returns tokens
POST /refresh      → rotates access token
POST /forgot-password → sends OTP
POST /reset-password  → sets new password
```

**Signup body:**
```json
{
  "full_name": "Ada Obi",
  "email": "ada@example.com",
  "password": "Secure1234",
  "dob": "1995-11-26",
  "gender": "Female",
  "account_type": "business",
  "business_type": "food"
}
```
`account_type` values: `personal` | `business`  
`business_type` values: `goods` | `food` | `service` | `fashion` | `health` | `education` | `logistics` | `other`

---

## Profile

```
GET  /profile                   → my full profile
PUT  /user/update               → update username / bio / full_name
POST /upload/profile            → upload profile photo (multipart, field: file)
POST /upload/header             → upload header photo  (multipart, field: file)
GET  /username/check?username=x → {"available": true}
GET  /user/:username            → public profile
```

---

## Posts & Interactions

```
POST   /post                    → create post (file + caption + post_type)
GET    /feed/public             → public feed
GET    /feed/following          → following feed

POST   /like/:post_id           → love/like a post
DELETE /like/:post_id           → unlike

POST   /reshare/:post_id        → reshare (shows in Reshared tab)
DELETE /reshare/:post_id        → remove reshare
GET    /reshares/:post_id       → reshare count

POST   /comment/:post_id        → add comment  { "content": "..." }
GET    /comments/:post_id       → get comments

POST   /save/:post_id           → save post
DELETE /save/:post_id           → unsave
```

---

## Shop / Products

Business accounts list products. Personal users can browse and buy.

```
# Public (no auth)
GET  /shop/products             → browse products (?category=food)

# Vendor (business account)
POST /shop/product              → create product (multipart)
  Fields: name, description, category, price, stock_count,
          is_unlimited_stock (true/false), images[] (up to 5 files)

GET  /shop/products/mine        → my products
```

### Stock logic
- `is_unlimited_stock: true` → services / digital / unlimited items — stock count ignored
- `is_unlimited_stock: false` → `stock_count` tracks remaining units, decremented on order

---

## Cart

```
POST   /shop/cart               → add to cart  { product_id, quantity }
GET    /shop/cart               → view cart + total
DELETE /shop/cart/:item_id      → remove item
```

---

## Checkout & Escrow Flow

```
POST /shop/checkout             → step 1: initialise payment
  Body: { product_id, quantity, delivery_date_scheduled (RFC3339, optional) }
  Returns: { reference, authorization_url, total }
         ↓
  (Mock: skip redirect, go straight to confirm)
  (Paystack: redirect user to authorization_url, wait for webhook/callback)
         ↓
POST /shop/checkout/confirm     → step 2: verify & create order
  Body: { product_id, quantity, delivery_date_scheduled, reference }
  Returns: { order, delivery_code }  ← buyer keeps delivery_code (show as QR)
```

### Escrow
- Money is **never sent directly to vendor**.
- On confirm, `escrow_amount` is recorded in `wallet_transactions` as `escrow_in`.
- Vendor only receives when they scan the delivery code.

---

## Orders

```
GET  /orders/mine?role=buyer    → my purchases
GET  /orders/mine?role=vendor   → my sales

POST /orders/:id/deliver        → vendor scans buyer's QR
  Body: { delivery_code }
  → order status: delivered
  → escrow_out credited to vendor wallet

# Breach (auto job — not a route)
  If delivery_date_scheduled passes with status=paid → status=breached → refund to buyer
```

---

## Cancel Order (3-party approval)

```
1. POST /orders/:id/cancel/request
   Buyer:  { "pin": "1234" }     ← buyer sets a verification pin
   → status stays "paid", cancel_requested_by = "buyer"

2. POST /orders/:id/cancel/vendor
   Vendor: no body               ← vendor signs off
   → vendor_cancel_approved = true

3. POST /orders/:id/cancel/admin
   Admin:  no body               ← admin finalises
   → status = "cancelled"
   → refund credited to buyer wallet as "refund" transaction
```

---

## Wallet

```
GET /wallet              → { available_balance, escrow_balance }
GET /wallet/history      → list of wallet_transactions
```

Transaction types:
| Type | Meaning |
|---|---|
| `escrow_in` | Buyer payment locked in escrow |
| `escrow_out` | Escrow released to vendor after delivery |
| `refund` | Escrow returned to buyer (breach or cancel) |
| `credit` | Direct top-up |
| `debit` | Withdrawal |

---

## Marketplace (Supply / Demand — separate from Shop)

```
POST /supply               → seller posts supply listing
GET  /supplies             → browse (?category=...)
GET  /supplies/mine        → my listings

POST /demand               → buyer posts what they want
GET  /demands              → browse
GET  /demands/mine         → my requests
```

---

## Contact Syncing (Phase 1)

```
POST /contacts/sync        → upload address book
  Body: { "contacts": [{"name": "Ada", "phone": "+2348031234567"}, ...] }
  → { synced_count, total_contacts, active_count, active_matches: [...] }
  Phone numbers are E.164-normalized (0803… = +234 803…) so formatting
  differences never break matching.

GET  /contacts             → my stored book, each entry flagged is_active
                             (matched_user attached when already on-platform)
DELETE /contacts           → wipe stored book (toggle stays on)

GET  /people-you-may-know  → suggestions: phone matches + mutual connections
                             ?limit=20 (max 50). Already-followed users are
                             excluded; users you follow are never suggested.

GET  /settings/contacts    → { contact_sync_enabled, contact_sync_at,
                              synced_count, active_count }
PUT  /settings/contacts    → toggle  { "contact_sync_enabled": true }
                             Disabling clears all stored contact data.
```

Phone matching is done on the server: contacts are stored with a sha256 of
the normalized number, and users with a matching number surface in
`active_matches` / `people-you-may-know`. Schema ships in `vinci.sql`, runs
automatically on server boot (self-healing migrations), and is also
available as `migration_contacts.sql` for existing databases.

Flutter: `lib/contacts/` contains the API models, `ContactSyncService`
(device address-book read via `flutter_contacts` + API calls),
`PeopleYouMayKnowScreen`, and `ContactSettingsScreen`. Set
`ApiClient.instance.token` from your auth flow before using the screens.
Add `READ_CONTACTS` (Android) / `NSContactsUsageDescription` (iOS) — both
are already configured in this repo.

---

## Messaging & Real-time

```
POST /message/send              → { receiver_id, content }
GET  /conversations             → list conversations
GET  /messages/:conv_id         → message history
GET  /ws                        → WebSocket upgrade (token in Authorization header)
```

---

## Payment Provider Swap

Located at `internal/config/payment.go`.

| Env | Provider |
|---|---|
| `PAYMENT_PROVIDER=mock` | Always succeeds — for testing |
| `PAYMENT_PROVIDER=paystack` | Fill in `InitializePayment` + `VerifyPayment` stubs |
| `PAYMENT_PROVIDER=flutterwave` | Fill in Flutterwave stubs |

The `PaymentProvider` interface is:
```go
type PaymentProvider interface {
    InitializePayment(req InitPaymentRequest) (*InitPaymentResponse, error)
    VerifyPayment(reference string) (*VerifyPaymentResponse, error)
    ProviderName() string
}
```
Implement a new struct, add a case to `NewPaymentProvider()`, done.

---

## Delivery Breach Cron

Run `ShopService.ProcessOverdueOrders()` on a schedule (e.g. every 15 min).
It finds all orders where `status=paid AND delivery_date_scheduled < NOW()`,
marks them `breached`, refunds the buyer, and restores stock.

Example with a goroutine in `main.go`:
```go
go func() {
    for range time.Tick(15 * time.Minute) {
        _ = shopService.ProcessOverdueOrders()
    }
}()
```

---

## Security Notes

- Passwords hashed with bcrypt (cost 12)
- JWTs short-lived (15 min access, 7 day refresh)
- OTPs stored in Redis with TTL (10 min)
- Rate limiter: 120 req/min per IP
- Upload paths validated; filenames sanitised
- Never expose raw DB errors to clients
- `cancel_buyer_pin` stored as bcrypt hash — never plaintext




## Remaining Features / Product Roadmap

> Status: **1. Contact syncing — shipped** (backend + Flutter, see above).
> Items below remain.

### 1. Contact Syncing
- Add contact import during signup and onboarding.
- Surface a “People You May Know” module using matched phone contacts and mutual connections.
- Add a settings toggle to control contact syncing and permission access.
- Show which contacts are already active on the platform.

### 2. Location & Nearby Discovery
- Show nearby content, profiles, and posts within a close radius or local government area (LGA).
- Add location-based filtering across commerce and marketplace discovery.
- Display business account locations on user profiles.
- Support chat location sharing: live location and one-time share.

### 3. Community Features
- During community creation, offer an invite flow for up to 5 people selected from followers and following.
- Let admins add other members as admins.
- Support owner-to-admin role transfer and admin permission controls.
- Add membership rules, visibility options, and admin moderation tools.

### 4. Demand Features
- On first use, show a quick onboarding popup explaining what demand is: thrift, second-hand discovery, and radius-based matching.
- Add location and distance filters to demand creation and discovery.
- After a demand is posted, show matching supply listings ranked by price, proximity, and relevance.
- Add an opt-in notification toggle for matching supply alerts.
- Set a short listing lifetime for demand posts, such as 2 days, to keep discovery fresh.

### 5. Supply Features
- On first use, show a lightweight onboarding prompt explaining that supply is for thrift or used items and matches demand by location.
- Let sellers configure a search radius or map-based distance and preview the estimated range while adjusting it.
- Add price and distance matching logic without forcing a heavy cost model at the start.
- Keep initial posting limits to reduce spam; make quota tuning easy to adjust later as the product matures.
- Add location-aware sorting and matching for demand discovery.
- If an item remains unsold after 2 days, allow it to be re-listed or refreshed for visibility.

### Recommended Priority
1. Contact syncing
2. Location and nearby discovery
3. Community role and admin flows
4. Demand matching experience
5. Supply posting limits and matching logic

This roadmap should be delivered in phases: first trust and onboarding, then discovery and local relevance, then community and marketplace matching, and finally advanced moderation and scaling controls.
