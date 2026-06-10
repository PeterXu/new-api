# Design: Add `{user_id}` Placeholder to Header Override

**Date**: 2026-06-10
**Status**: Approved

## Summary

Add `{user_id}` as a new placeholder in the existing `header_override` system, allowing admins to inject the requesting user's ID into custom headers sent to upstream API providers.

## Background

The channel `header_override` feature already supports placeholders:
- `{api_key}` — replaced with the channel's API key
- `{client_header:<name>}` — replaced with a header from the incoming client request

Users want to inject user IDs into request headers for similar purposes as "Inject User ID in Proxy" — per-user identification at the upstream provider (e.g., for billing attribution, rate limiting, or logging).

## Design

### Placeholder Syntax

Add `{user_id}` as a new placeholder. Usage example:

```json
{
  "X-User-Id": "{user_id}",
  "X-Customer-Id": "{user_id}",
  "X-Trace-User": "user-{user_id}"
}
```

### Implementation Location

**File**: `relay/channel/api_request.go`
**Function**: `applyHeaderOverridePlaceholders()`

The function already handles `{api_key}` substitution. Add `{user_id}` substitution using `info.UserId`.

### Data Flow

1. Admin configures channel with header_override JSON containing `{user_id}` placeholder
2. During relay, `processHeaderOverride()` is called in `DoApiRequest()`/`DoFormRequest()`/`DoWssRequest()`
3. For each header value, `applyHeaderOverridePlaceholders()` is called
4. If `{user_id}` is present, it's replaced with `strconv.Itoa(info.UserId)`
5. Resulting header is added to upstream request

### Edge Cases

- **UserId == 0**: Placeholder resolves to `"0"`. This is valid (channel test requests, system operations).
- **Empty after substitution**: Existing behavior — header is skipped if value is empty/whitespace only.

## Implementation Plan

1. Modify `applyHeaderOverridePlaceholders()` to accept `userId int` parameter
2. Add `{user_id}` substitution logic
3. Update callers to pass `info.UserId`
4. Update function comment documentation
5. Update frontend template example to show `{user_id}`
6. Add unit test for `{user_id}` placeholder
7. Update i18n strings if needed

## Files to Modify

| File | Change |
|------|--------|
| `relay/channel/api_request.go` | Add `{user_id}` placeholder support |
| `relay/channel/api_request_test.go` | Add unit tests |
| `web/default/src/features/channels/components/drawers/channel-mutate-drawer.tsx` | Update template example and documentation |
| `web/classic/src/components/table/channels/modals/EditChannelModal.jsx` | Update template example and documentation |
| `web/classic/src/components/table/channels/modals/EditTagModal.jsx` | Update documentation |

## Testing

- Unit test: `applyHeaderOverridePlaceholders()` with `{user_id}` returns correct value
- Unit test: `{user_id}` with userId=0 returns "0"
- Integration: Configure channel with `{user_id}` header, verify header appears in upstream request

## Security Considerations

- User ID is numeric and safe for header injection (no XSS risk)
- Admin-controlled setting, not user-controlled (no spoofing risk)
