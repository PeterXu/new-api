# proxyClients Lifecycle Management

## Overview

Proxy HTTP clients are cached in a global map for reuse across relay requests. This document describes the lifecycle: creation, lookup, and targeted cleanup.

## Data Structure

```
proxyClients map[string]*http.Client  // key = proxyURL, value = cached *http.Client
proxyClientLock sync.RWMutex          // protects proxyClients map
```

- **Channel proxy URL**: the `Proxy` field from `ChannelSettings` (stored in channel's `Setting` JSON column).
- **Actual proxy URL**: when `InjectUserIdInProxyURL` is true, user ID is appended to the proxy URL's username (e.g., `socks5://user@42:pass@host:port`), so each user gets a distinct cached client.
- These are usually the same; they differ only when `InjectUserIdInProxyURL` is enabled.

## Map Operations

| Operation | Function | Lock | Notes |
|-----------|----------|------|-------|
| Create | `NewProxyHttpClient()` | RLock (check) → Lock (insert) | Double-checked locking with race guard |
| Lookup | `NewProxyHttpClient()` | RLock | Returns cached client if exists |
| Remove single | `removeProxyClient()` | Lock | Deletes one key, closes idle connections outside lock |
| Remove injected pattern | `removeInjectedProxyClients()` | Lock | Deletes all `baseUser@*` keys, closes idle connections outside lock |
| Reset all | `ResetProxyClientCache()` | Lock | Clears entire map (legacy, being phased out) |

## Cleanup Architecture

### Targeted Cleanup Flow

```
Channel lifecycle event
  └─→ CleanupChannelProxy(channelId) / CleanupChannelProxyBatch(channelIds) / CleanupChannelProxyConfig(proxyURL, injectUserId, excludeId)
        └─→ GetChannelProxyConfig(channel) → extract proxy URL + injectUserId
              └─→ cleanupProxy(proxyURL, injectUserId, excludeSet)
                    └─→ isProxyURLUsedByEnabledChannels(proxyURL, injectUserId, excludeSet)
                          │ true  → keep client (other channels still use it)
                          │ false → removeProxyClient() or removeInjectedProxyClients()
```

### Key Functions

| Function | Purpose |
|----------|---------|
| `CleanupChannelProxy(channelId)` | Cleanup for a single channel (delete, disable, codex OAuth) |
| `CleanupChannelProxyConfig(proxyURL, injectUserId, excludeId)` | Cleanup with known config (channel update — old config captured before update) |
| `CleanupChannelProxyBatch(channelIds)` | Cleanup for batch delete (deduplicates proxy URLs across channels) |
| `cleanupProxy(proxyURL, injectUserId, excludeSet)` | Core logic: check if proxy still used, remove if not |
| `isProxyURLUsedByEnabledChannels(...)` | Checks in-memory cache or DB for other enabled channels using same proxy |
| `GetChannelProxyConfig(channel)` | Extracts `Proxy` and `InjectUserIdInProxyURL` from channel's `Setting` JSON |

### `GetAllEnabledChannels()` — Data Source

`isProxyURLUsedByEnabledChannels` calls `model.GetAllEnabledChannels()` which:
- Uses in-memory cache (`channelsIDM`) under read lock when `MemoryCacheEnabled`
- Falls back to DB query with GORM error handling when cache is disabled
- On error: conservatively assumes proxy is still in use (prevents accidental cleanup)

## Cleanup Trigger Points

| Event | Location | Cleanup Called |
|-------|----------|----------------|
| Delete channel | `controller/channel.go:DeleteChannel()` | `CleanupChannelProxy(id)` **before** DB delete |
| Batch delete | `controller/channel.go:DeleteChannelBatch()` | `CleanupChannelProxyBatch(ids)` **before** DB delete |
| Update channel | `controller/channel.go:UpdateChannel()` | `CleanupChannelProxyConfig(oldURL, oldInject, id)` **after** DB update |
| Disable by tag | `controller/channel.go:DisableTagChannels()` | `CleanupChannelProxyBatch(ids)` **before** DB update |
| Auto-disable | `service/channel.go:DisableChannel()` | `CleanupChannelProxy(channelId)` after status update |
| Codex OAuth save | `controller/codex_oauth.go:completeCodexOAuthWithChannelID()` | `CleanupChannelProxy(channelID)` after cache refresh |
| Codex usage refresh | `controller/codex_usage.go:GetCodexChannelUsage()` | `CleanupChannelProxy(ch.Id)` after cache refresh |
| Upstream model update | `controller/channel_upstream_update.go:refreshChannelRuntimeCache()` | **No cleanup** — only model lists change, not proxy settings |
| Codex credential refresh | `service/codex_credential_refresh.go` | **No cleanup** — only key data changes, not proxy settings |
| Codex credential refresh task | `service/codex_credential_refresh_task.go` | **No cleanup** — only key data changes, not proxy settings |
| Batch insert channels | `controller/channel.go:AddChannel()` | **No cleanup** — new channels create clients lazily |
| Edit by tag | `controller/channel.go:EditTagChannels()` | **No cleanup** — tag edits don't touch `Setting` field (proxy config) |
| Delete disabled channels | `controller/channel.go:DeleteDisabledChannel()` | **No cleanup** — proxy clients already removed when channels were disabled |

## Creation Points (Lazy — clients created on first use)

| File | Scenario | User ID Injection? |
|------|----------|-------------------|
| `relay/channel/api_request.go` | Main relay requests | YES (when `InjectUserIdInProxyURL`) |
| `relay/channel/aws/relay-aws.go` | AWS Bedrock relay | YES (via `info.ChannelSetting`) |
| `relay/channel/coze/relay-coze.go` | Coze relay | YES |
| `relay/channel/vertex/service_account.go` | Vertex AI | YES |
| `relay/channel/task/*` | Task adaptors (sora, suno, etc.) | NO |
| `relay/channel/gemini/relay-gemini.go` | Gemini relay | NO |
| `relay/mjproxy_handler.go` | MJ proxy | NO |
| `controller/channel-billing.go` | Billing usage fetch | NO |
| `controller/codex_usage.go` | Codex usage fetch | NO |
| `controller/video_proxy.go` | Video proxy | NO |

## User ID Injection Handling

```
Channel Setting:       Proxy = "socks5://user:pass@host:port", InjectUserIdInProxyURL = true
Actual cached keys:    "socks5://user@1:pass@host:port"  (user 1)
                       "socks5://user@2:pass@host:port"  (user 2)
                       "socks5://user@42:pass@host:port" (user 42)
                       ... potentially thousands of active users
```

- `common.InjectUserIdInProxyURL()` appends `@{userId}` to the username part.
- `removeInjectedProxyClients(baseURL)` finds all cached keys matching `baseUser@*` at the same host with same password.
- This is critical for cleanup: deleting/disabling one channel with injection can remove thousands of cached clients.

## Connection vs Client Lifecycle

| Resource | Lifecycle | Cleanup |
|----------|-----------|---------|
| TCP connections | 90s idle timeout | `IdleConnTimeout` in `http.Transport` |
| `http.Client` objects | Removed when no enabled channel uses the proxy | `removeProxyClient()` / `removeInjectedProxyClients()` |
| Entire cache | Only via `ResetProxyClientCache()` (legacy) | Being phased out in favor of targeted cleanup |

## Lock Contention Notes

- `removeProxyClient()` and `removeInjectedProxyClients()` collect transports under write lock but call `CloseIdleConnections()` **after** releasing the lock.
- `NewProxyHttpClient()` uses read lock for cache lookup, write lock only for insertion (with double-check to avoid races).
- `isProxyURLUsedByEnabledChannels()` reads channel cache under its own read lock — no interaction with proxy client lock.
