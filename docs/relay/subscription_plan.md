# Subscription Plan System

## Overview

The subscription system lets admins define recurring plans that users purchase for a fixed duration. Each plan allocates a **quota budget** (in internal quota units) that is consumed when the user makes AI API requests. Plans can also upgrade the user's group (unlocking model access or pricing tiers).

## Core Models

### SubscriptionPlan (admin-defined template)

| Field | Type | Description |
|---|---|---|
| `title` | string | Display name shown to users |
| `subtitle` | string | Secondary description |
| `price_amount` | float64 | Price in `currency` (e.g. 9.99 USD) |
| `currency` | string | ISO currency code (default: `USD`) |
| `duration_unit` | string | `year` / `month` / `day` / `hour` / `custom` |
| `duration_value` | int | Multiplier for duration_unit (e.g. `3` + `month` = 3 months) |
| `custom_seconds` | int64 | Used only when `duration_unit=custom` |
| `enabled` | bool | Whether plan is purchasable |
| `allow_balance_pay` | *bool | Whether user can pay via wallet balance (default: true) |
| `stripe_price_id` | string | Stripe Price ID for payment |
| `creem_product_id` | string | Creem Product ID for payment |
| `waffo_pancake_product_id` | string | WaffoPancake Product ID |
| `max_purchase_per_user` | int | Max times one user can buy this plan (0 = unlimited) |
| `upgrade_group` | string | User group to assign on purchase (empty = no change) |
| **`total_amount`** | **int64** | **Quota budget per reset period (0 = unlimited)** |
| `quota_reset_period` | string | `never` / `daily` / `weekly` / `monthly` / `custom` |
| `quota_reset_custom_seconds` | int64 | Custom reset interval in seconds |

### UserSubscription (per-user instance)

| Field | Type | Description |
|---|---|---|
| `user_id` | int | Owner |
| `plan_id` | int | Source plan |
| `amount_total` | int64 | Quota budget per reset period (copied from plan at creation) |
| `amount_used` | int64 | Quota consumed so far in current period (reset to 0 on reset) |
| `start_time` | int64 | When subscription becomes active |
| `end_time` | int64 | When subscription expires |
| `status` | string | `active` / `expired` / `cancelled` |
| `source` | string | `order` (paid) or `admin` (manually assigned) |
| `last_reset_time` | int64 | Timestamp of last quota reset |
| `next_reset_time` | int64 | Timestamp of next scheduled reset |
| `upgrade_group` | string | Group assigned by this subscription |
| `prev_user_group` | string | User's group before upgrade (restored on expiry) |

### SubscriptionOrder (payment record)

| Field | Type | Description |
|---|---|---|
| `user_id` | int | Buyer |
| `plan_id` | int | Purchased plan |
| `money` | float64 | Amount charged |
| `trade_no` | string | Unique transaction ID |
| `payment_method` | string | How the user paid |
| `payment_provider` | string | Gateway (stripe, creem, epay, waffo_pancake, balance) |
| `status` | string | `pending` / `success` / `expired` |

### SubscriptionPreConsumeRecord (idempotency guard)

| Field | Type | Description |
|---|---|---|
| `request_id` | string | Relay request ID (unique index) |
| `user_subscription_id` | int | Which subscription was debited |
| `pre_consumed` | int64 | Amount reserved |
| `status` | string | `consumed` / `refunded` |

## Quota Units

`total_amount` is measured in **internal quota units**. It is the hard cap on how much API resource a user can consume per reset period. Every API call consumes quota — the amount consumed depends on which model is used and how many tokens are processed.

### Core Principle

```
每次 API 调用 → 消耗一定额度（取决于模型 + token 数）
额度用完     → 该周期内无法继续调用
额度重置     → 恢复到 total_amount，可以继续使用
```

So `total_amount: 1000` and `total_amount: 100000` are fundamentally different — the user with 1000 can make far fewer API calls than the user with 100000.

### How Quota Is Consumed Per Request

Each API request consumes quota calculated from actual token usage. The core formula (`service/quota.go:calculateAudioQuota`):

```
quota = (inputTextTokens
       + outputTextTokens  × CompletionRatio
       + inputAudioTokens  × AudioRatio
       + outputAudioTokens × AudioRatio × AudioCompletionRatio)
      × ModelRatio × GroupRatio
```

Where each multiplier is defined in:

| Multiplier | Source | Description |
|---|---|---|
| `ModelRatio` | `setting/ratio_setting/model_ratio.go` | Per-model price multiplier. Base = 1 = $0.002/1K tokens |
| `CompletionRatio` | `setting/ratio_setting/completion_ratio.go` | Output tokens cost premium (e.g. 2x, 3x) |
| `AudioRatio` | `setting/ratio_setting/audio_ratio.go` | Audio token price multiplier |
| `AudioCompletionRatio` | `setting/ratio_setting/audio_ratio.go` | Audio output premium |
| `GroupRatio` | `relay/helper/price.go:HandleGroupRatio` | Group-based pricing discount (e.g. pro group = 0.8) |

For models configured with a fixed price (`UsePrice = true`), the formula simplifies to:

```
quota = ModelPrice × QuotaPerUnit × GroupRatio
```

### Core Constant

Defined in `common/constants.go`:

```
QuotaPerUnit = 500,000    // $1 USD = 500,000 quota
```

### Money ↔ Quota

```
Quota = USD × 500,000
USD   = Quota / 500,000
```

This conversion is used when purchasing subscriptions via wallet balance (`requiredQuota = ceil(priceAmount * QuotaPerUnit)`) and when displaying quota to users in the UI.

### Model Pricing Examples

| Model | ModelRatio | Cost per 1K tokens |
|---|---|---|
| gpt-4o-mini | 0.075 | $0.00015 |
| gpt-3.5-turbo | 0.25 | $0.0005 |
| claude-3-opus | 7.5 | $0.015 |
| gpt-4 | 15 | $0.03 |

### Concrete Example

`total_amount: 500,000` = $1 USD equivalent. The actual number of API calls this buys depends entirely on the model:

| Model | Tokens available | Approx. requests (1K tokens each) |
|---|---|---|
| gpt-4o-mini | ~6.7M | ~6,700 |
| gpt-3.5-turbo | ~2M | ~2,000 |
| claude-3-opus | ~67K | ~67 |
| gpt-4 | ~33K | ~33 |

### Pre-Consume Check

Before each request, the system checks whether the subscription has enough quota (`model/subscription.go:PreConsumeUserSubscription`):

```
remain = amount_total - amount_used
if remain < requested_quota → skip this subscription, try next or fallback to wallet
```

## Billing Flow During API Requests

### Billing Preference

Users choose how to prioritize funding sources:

| Preference | Behavior |
|---|---|
| `subscription_first` (default) | Try subscription, fallback to wallet |
| `wallet_first` | Try wallet, fallback to subscription |
| `subscription_only` | Only use subscription, fail if none/unfunded |
| `wallet_only` | Only use wallet, skip subscription entirely |

### Request Lifecycle

```
1. PRE-CONSUME (before relay)
   ├── Estimate tokens for the request
   ├── Check billing preference
   ├── Attempt funding source (subscription or wallet)
   │   ├── Subscription: PreConsumeUserSubscription()
   │   │   ├── Find active subscriptions (ordered by end_time asc)
   │   │   ├── Check quota reset if next_reset_time passed
   │   │   ├── Verify amount_total - amount_used >= requested
   │   │   ├── Create idempotent PreConsumeRecord
   │   │   └── Increment amount_used
   │   └── Wallet: DecreaseUserQuota()
   │       └── Deduct from user's wallet balance
   └── If primary source fails, try fallback (per preference)

2. RELAY (execute upstream API call)
   └── Request sent to AI provider

3. SETTLE (after relay completes)
   ├── Calculate delta = actualQuota - preConsumedQuota
   ├── Positive delta: charge additional from funding source
   ├── Negative delta: refund difference to funding source
   ├── Update subscription amount_used or wallet balance
   └── Send quota notification to user

4. REFUND (on relay failure)
   └── Restore pre-consumed amount to funding source
```

### Quota Reset Behavior

When `quota_reset_period` is not `never`, a background task (runs every 60s) checks `next_reset_time`:

| Period | Reset Boundary | Example |
|---|---|---|
| `daily` | Next midnight UTC | Purchased at 3pm, resets at midnight |
| `weekly` | Next Monday 00:00 UTC | Purchased on Wednesday, resets next Monday |
| `monthly` | 1st of next month 00:00 UTC | Purchased Jan 15, resets Feb 1 |
| `custom` | Every N seconds from last reset | `quota_reset_custom_seconds=3600` = hourly |

On reset: `amount_used` is set to 0, `next_reset_time` is recalculated. If the next reset would exceed `end_time`, no further resets are scheduled.

### Group Upgrade/Downgrade

When a plan has `upgrade_group` set:
1. **On purchase**: User's current group is saved to `prev_user_group`, then user is moved to `upgrade_group`
2. **On expiry**: If no other active subscription provides the same upgrade, user is reverted to `prev_user_group`

## Subscription Differentiation

Different subscription plans control user access through **two orthogonal dimensions**:

### 1. Quota Dimension (`total_amount` + `quota_reset_period`) — How Many API Calls

`total_amount` is the hard cap on API consumption per reset period. Every API call deducts from it based on the model's cost formula (see Quota Units above). When it runs out, the user cannot make more API calls until the next reset.

| Plan | Quota/Period | What it actually means |
|---|---|---|
| Trial (50K) | 50K total | ~7K tokens with gpt-4, or ~670K tokens with gpt-4o-mini |
| Basic (500K/week) | 500K per week | ~33K tokens/week with gpt-4, or ~6.7M tokens/week with gpt-4o-mini |
| Pro (10M/year) | 10M total | ~670K tokens with gpt-4, or ~133M tokens with gpt-4o-mini |

`total_amount` directly controls the number of API calls a user can make. There is no other usage cap.

### 2. Permission Dimension (`upgrade_group`) — What Models, What Price

`upgrade_group` controls **which models** the user can access and **at what price multiplier**:

- **Model access**: Each model has an `enable_groups` list. Only users whose group is in that list can call the model.
- **Pricing multiplier**: Each group has a `group_ratio` (applied via `relay/helper/price.go:HandleGroupRatio`). For example, a "pro" group might have ratio 0.8 (20% discount), making all models cheaper for pro subscribers.

```
Basic plan:  upgrade_group = ""      → default group, limited models, standard pricing
Pro plan:    upgrade_group = "pro"   → pro group, premium models, discounted pricing
Trial plan:  upgrade_group = "trial" → trial group, restricted model set
```

On purchase, the user's current group is saved to `prev_user_group`, then they are moved to `upgrade_group`. On expiry, they revert.

### Subscription Selection (Multiple Active Subscriptions)

When a user has multiple active subscriptions, the system charges them in order of `end_time ASC` (soonest to expire first). Users can also set a `billing_preference` to control which funding source is tried first:

| Preference | Behavior |
|---|---|
| `subscription_first` (default) | Try subscription, fallback to wallet |
| `wallet_first` | Try wallet, fallback to subscription |
| `subscription_only` | Only subscription, fail if unfunded |
| `wallet_only` | Only wallet, skip subscriptions |

## Example Scenarios

### Example 1: Basic Monthly Plan with Weekly Quota Reset

```json
{
  "title": "Basic Plan",
  "price_amount": 9.99,
  "currency": "USD",
  "duration_unit": "month",
  "duration_value": 1,
  "total_amount": 500000,
  "quota_reset_period": "weekly",
  "upgrade_group": "",
  "max_purchase_per_user": 0,
  "allow_balance_pay": true
}
```

**Behavior:** User pays $9.99/month. Gets 500,000 quota units per week (resets every Monday 00:00 UTC). No group upgrade. User can buy multiple times.

- Week 1: `amount_used` resets to 0 at next Monday
- Week 2: Fresh 500,000 budget
- After 1 month: Subscription expires, no further resets

### Example 2: Annual Plan with Group Upgrade, No Reset

```json
{
  "title": "Pro Annual",
  "price_amount": 99.0,
  "currency": "USD",
  "duration_unit": "year",
  "duration_value": 1,
  "total_amount": 10000000,
  "quota_reset_period": "never",
  "upgrade_group": "pro",
  "max_purchase_per_user": 1
}
```

**Behavior:** User pays $99/year. Gets 10M quota units for the entire year (no resets). User is moved to group "pro" (unlocking pro-tier models/pricing). Limited to 1 purchase per user. On expiry, user reverts to their previous group.

### Example 3: Hourly Trial Plan

```json
{
  "title": "2-Hour Trial",
  "price_amount": 0,
  "currency": "USD",
  "duration_unit": "hour",
  "duration_value": 2,
  "total_amount": 50000,
  "quota_reset_period": "never",
  "upgrade_group": "trial",
  "max_purchase_per_user": 1
}
```

**Behavior:** Free 2-hour trial with 50K quota. User upgraded to "trial" group. Admin assigns this via dashboard (no payment needed since price is 0). Expires after 2 hours.

### Example 4: Custom Duration with Custom Reset

```json
{
  "title": "Sprint Plan",
  "price_amount": 4.99,
  "currency": "USD",
  "duration_unit": "custom",
  "custom_seconds": 259200,
  "total_amount": 200000,
  "quota_reset_period": "custom",
  "quota_reset_custom_seconds": 43200,
  "upgrade_group": ""
}
```

**Behavior:** 3-day subscription (259,200 seconds) with quota resetting every 12 hours (43,200 seconds). 200K quota per 12-hour window.

## Purchase Flows

### Via Payment Provider (Stripe, Creem, Epay, WaffoPancake)

```
User selects plan
  → Frontend calls payment API (e.g. /api/subscription/pay/stripe)
  → Backend creates SubscriptionOrder (status=pending)
  → Returns payment URL/checkout session
  → User completes payment
  → Payment provider sends webhook
  → Backend calls CompleteSubscriptionOrder()
    → Transaction: mark order success + CreateUserSubscriptionFromPlanTx()
    → If upgrade_group: update user group
    → Log purchase
```

### Via Wallet Balance

```
User selects plan (allow_balance_pay=true)
  → Frontend calls /api/subscription/pay/balance
  → Backend calls PurchaseSubscriptionWithBalance()
    → Transaction:
      → Verify plan enabled and balance sufficient
      → Deduct wallet: quota -= ceil(price * QuotaPerUnit)
      → CreateUserSubscriptionFromPlanTx()
      → Create SubscriptionOrder (status=success, payment_method=balance)
    → Update user quota cache
    → If upgrade_group: update user group cache
```

### Via Admin Assignment

```
Admin selects user + plan
  → Backend calls AdminBindSubscription()
    → Transaction: CreateUserSubscriptionFromPlanTx() with source="admin"
    → No payment, no order
    → If upgrade_group: update user group cache
```

## Background Tasks

The `StartSubscriptionQuotaResetTask()` runs on the master node every 60 seconds:

1. **Expire subscriptions**: Mark subscriptions where `end_time <= now` as `expired`. Downgrade user group if no other active subscription provides the upgrade.
2. **Reset quotas**: For subscriptions where `next_reset_time <= now`, reset `amount_used` to 0 and recalculate `next_reset_time`.
3. **Cleanup records**: Every 30 minutes, delete `SubscriptionPreConsumeRecord` rows older than 7 days.

## Caching

| Cache | Key | TTL | Invalidated On |
|---|---|---|---|
| Plan cache | `plan:{id}` | 300s (configurable) | Plan update/delete |
| Plan info cache | `sub:{userSubscriptionId}` | 120s (configurable) | Plan update (full purge) |

Both use a hybrid cache: Redis (if enabled) + in-memory LRU. Cache capacities and TTLs are configurable via environment variables.

## Current Limitations

1. **No per-minute token rate limits** (ITPM/OTPM/TPM) -- only total quota budgets exist today
2. **Single quota window** -- only one reset period per plan (cannot combine hourly + weekly + monthly caps)
3. **No per-model limits** -- quota is shared across all models the subscription can access
4. **Price and quota are independently configured** -- admins must manually balance `price_amount` vs `total_amount` to achieve desired profit margins; there is no automatic derivation
