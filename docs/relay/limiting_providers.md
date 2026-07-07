# Provider Rate Limiting Comparison

Comparison of rate limiting policies from major AI API providers.

---

## Rate Limit Dimensions

| Dimension | Anthropic | ZhipuAI (big-model.cn) | MiniMax |
|---|---|---|---|
| **Requests/min** | RPM | — | RPM |
| **Input tokens/min** | ITPM | — | — |
| **Output tokens/min** | OTPM | — | — |
| **Tokens/min** | — | — | TPM |
| **Concurrent requests** | Yes (undocumented limits) | Primary dimension | Only music |
| **Daily/weekly quotas** | Monthly spend limit | Daily calls, weekly/monthly caps | 5h + weekly Token Plan |

Anthropic uniquely splits TPM into **ITPM** (input) and **OTPM** (output). Output tokens are more expensive (compute-intensive generation vs. cheap prompt processing). A single long-generation request could exhaust OTPM while ITPM has plenty of headroom.

---

## Algorithm

| | Anthropic | ZhipuAI | MiniMax |
|---|---|---|---|
| **Algorithm** | Token bucket | Concurrency counter | Sliding window |
| **Behavior** | Capacity replenishes continuously | Instantaneous in-flight count | Counts requests in past 60s |

Token bucket (Anthropic) allows a full burst up to the limit immediately after replenishment, then enforces a steady-state rate. Sliding window (MiniMax) evenly spreads allowed requests across the window. Anthropic notes that 60 RPM may be internally enforced as 1 req/sec, so concentrated bursts can still fail.

---

## Spending-Based Auto-Tiering

### Anthropic

5 tiers by credit purchase ($5 → $400). Advance immediately on reaching threshold.

| Usage Tier | Credit Purchase Required | Max Single Credit Purchase | Monthly Spend Limit |
|---|---|---|---|
| **Tier 1** | $5 | $500 | $500/month |
| **Tier 2** | $40 | $500 | $500/month |
| **Tier 3** | $200 | $1,000 | $1,000/month |
| **Tier 4** | $400 | $200,000 | $200,000/month |
| **Monthly Invoicing** | N/A | N/A | No limit |

**Opus-class limits by tier:**

| Tier | ITPM | OTPM |
|---|---|---|
| Tier 1 | 500,000 | 80,000 |
| Tier 2 | 2,000,000 | 400,000 |
| Tier 3 | 5,000,000 | 1,000,000 |
| Tier 4 | 10,000,000 | 2,000,000 |

Limits are per organization, per model family (Opus/Sonnet/Haiku have independent limits).

### ZhipuAI

6 tiers by monthly spend (¥0 → ¥30K+).

| Tier | Monthly Spend (CNY) |
|---|---|
| Free | 0–50 |
| L1 | 50–500 |
| L2 | 500–5,000 |
| L3 | 5,000–10,000 |
| L4 | 10,000–30,000 |
| L5 | >30,000 |

**Per-model concurrency limits:**

| Model | Free | L1 | L2 | L3 | L4 | L5 |
|---|---|---|---|---|---|---|
| GLM-4 | 5 | 10 | 20 | 30 | 100 | 200 |
| GLM-4-Air | 5 | 50 | 70 | 150 | 300 | 1000 |
| GLM-4-Flash | 5 | 10 | 50 | 100 | 200 | 300 |
| CogVideoX | 1 | 2 | 3 | 4 | 5 | 6 |

### MiniMax

2 tiers (Free / Paid). Per-model RPM/TPM.

| Model | RPM (Free) | TPM (Free) | RPM (Paid) | TPM (Paid) |
|---|---|---|---|---|
| MiniMax-M3 | 20 | 1,000,000 | 200 | 10,000,000 |
| M2.7 / M2.5 / M2.1 | 20 | 1,000,000 | 500 | 20,000,000 |
| Video (Hailuo) | 5 | — | 20 | — |
| Speech (T2A) | 10 | — | 20 | — |
| Image | 10 | 60 | 10 | 60 |
| Music | 3 RPM / 3 CONN | — | 120 RPM / 20 CONN | — |

---

## Multi-Window Patterns

| | Anthropic | ZhipuAI | MiniMax |
|---|---|---|---|
| **Short-term** | RPM + ITPM + OTPM per minute (token bucket) | Concurrent (instant) | RPM/TPM per minute |
| **Medium-term** | — | 5h rolling token quota (Coding Plan) | 5h Token Plan quota |
| **Long-term** | Monthly spend limit | Daily calls, weekly/monthly caps | Weekly Token Plan quota |

All providers layer multiple independent windows — a request must pass all simultaneously.

---

## Response Headers

| Header | Anthropic | ZhipuAI | MiniMax |
|---|---|---|---|
| `retry-after` | Yes (on 429) | No | No |
| `ratelimit-requests-limit` | Yes | No | No |
| `ratelimit-requests-remaining` | Yes | No | No |
| `ratelimit-requests-reset` | Yes (RFC 3339) | No | No |
| `ratelimit-input-tokens-*` | Yes (limit/remaining/reset) | No | No |
| `ratelimit-output-tokens-*` | Yes (limit/remaining/reset) | No | No |
| `ratelimit-tokens-*` | — | — | — |

Anthropic returns **13 rate-limit headers on every response** (not just 429s). This lets clients proactively throttle before hitting limits. No other provider does this.

---

## Error Codes

### Anthropic

| HTTP Status | Error Type | Meaning |
|---|---|---|
| **429** | `rate_limit_error` | Rate limit exceeded (includes `retry-after` header) |
| **529** | `overloaded_error` | API temporarily overloaded (distinct from rate limiting) |

Error response format:
```json
{
  "type": "error",
  "error": {
    "type": "rate_limit_error",
    "message": "Rate limit exceeded. Please retry after 60 seconds."
  }
}
```

### ZhipuAI

| Code | Meaning |
|---|---|
| 1302 | Account rate limit reached (concurrency exceeded) |
| 1303 | High-frequency usage (request rate too fast) |
| 1304 | Daily call limit reached |
| 1305 | Platform service overload |
| 1308 | Window usage limit reached, includes `next_flush_time` |
| 1309 | GLM Coding Plan subscription expired |
| 1310 | Weekly/monthly limit reached, includes `next_flush_time` |
| 1311 | Current plan does not support this model |
| 1312 | Model overloaded, suggests alternative models |
| 1313 | Fair use policy violation |

### MiniMax

| Code | Meaning |
|---|---|
| 1002 | Rate limit (请求频率超限) |
| 1039 | Token limit |
| 1041 | Connection limit |
| 2045 | Rate growth limit (spike detection) |
| 2056 | Usage limit exceeded (Token Plan) |

---

## Burst / Spike Detection

| | Anthropic | ZhipuAI | MiniMax |
|---|---|---|---|
| **Acceleration limits** | Yes — detects sharp usage spikes, triggers 429 independently of published limits | Error 1313 (fair use policy) | Error 2045 (rate growth limit) |
| **Dynamic throttling** | No documented peak-hour throttling | Yes (weekdays 15:00–18:00 Beijing) | Yes (peak hours) |
| **Recommendation** | "Ramp up traffic gradually" | — | — |

All three providers penalize request pattern anomalies, not just absolute thresholds.

---

## Cache-Aware Rate Limiting

Anthropic has a unique optimization: **cached input tokens don't count toward ITPM** (except Haiku 3.5).

| Token Type | Counts Toward ITPM? |
|---|---|
| `input_tokens` (after last cache breakpoint) | Yes |
| `cache_creation_input_tokens` | Yes |
| `cache_read_input_tokens` | **No** (except Haiku 3.5) |

With an 80% cache hit rate and 2M ITPM limit, effective throughput is ~10M input tokens/minute.

---

## Service Tiers

| | Anthropic | ZhipuAI | MiniMax |
|---|---|---|---|
| **Standard** | Default | Default | Default |
| **Priority** | Paid commitment (1–12 month), 99.5% uptime target | — | — |
| **Batch** | Async, 50% cost reduction, separate RPM | Yes (no concurrency limit, 50% cheaper) | — |

---

## Gap Analysis

| Gap | Who does it | Status |
|---|---|---|
| **Rate-limit response headers** (`retry-after`, remaining, reset) | Anthropic only | **Low priority** |
| **Split TPM into ITPM/OTPM** | Anthropic only | **Low priority** |
| **Concurrent request limiting** | ZhipuAI (primary), Anthropic (undocumented) | **Low priority** |
| **Cache-aware rate limiting** | Anthropic only | **Low priority** — only relevant with prompt caching proxy |
| **Acceleration/burst detection** | All three providers | **Low priority** — complex to implement |
| **Spending-based auto-tiering** | Anthropic, ZhipuAI | **Low priority** — admin-managed, not direct-to-consumer |
| **Priority/capacity reservation** | Anthropic (Priority Tier) | **Low priority** — enterprise feature |

---

## Sources

- [Anthropic Rate Limits](https://platform.claude.com/docs/en/api/rate-limits)
- [Anthropic Errors](https://platform.claude.com/docs/en/api/errors)
- [Anthropic Service Tiers](https://platform.claude.com/docs/en/api/service-tiers)
- [ZhipuAI Rate Limits (Chinese)](https://docs.bigmodel.cn/cn/api/rate-limit)
- [ZhipuAI Rate Limits (English)](https://bigmodel.cn/dev/howuse/rate-limits)
- [ZhipuAI Error Codes](https://docs.bigmodel.cn/cn/faq/api-code)
- [MiniMax Rate Limits (Chinese)](https://platform.minimaxi.com/docs/guides/rate-limits)
- [MiniMax Rate Limits (English)](https://platform.minimax.io/docs/guides/rate-limits)
- [MiniMax Error Codes](https://platform.minimax.io/docs/api-reference/errorcode)

