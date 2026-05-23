# BYOK (Bring Your Own Key)

## Overview

BYOK lets you use your own OpenAI / Anthropic API key directly with AI Gateway. Requests are still routed through the gateway (with the same OpenAI/Claude-compatible endpoints, model routing, and observability you'd expect), but your provider charges go to your own provider account rather than against the shared admin-configured channels. The gateway will always prefer your BYOK channels over the shared pool; if your key fails or no BYOK channel exists for the requested model, it falls back to shared admin channels.

## End-User Guide

### Add a private channel

1. Open **BYOK** in the sidebar (or click the BYOK card on your profile page).
2. Click **New**.
3. Pick a provider (OpenAI / Anthropic / etc.).
4. Paste your API key. The last 4 characters appear below the field so you can verify; plaintext is never displayed again.
5. Pick the models you want this key to serve.
6. Save.

### View usage

Go to **BYOK → Usage Stats**. You see total requests and (in service-fee mode) the gateway service cost. By default, BYOK requests do not deduct from your gateway quota.

### Reset your key

Edit a channel → enter a new key → save. The new ciphertext replaces the old one; agents drop their cache automatically and start using the new key on the next request.

## Security Model

- **Encryption at rest**: keys are stored as AES-256-GCM ciphertext in the master DB. The DB contains only ciphertext + the last 4 characters for display.
- **Decryption location**: master decrypts the key and pushes plaintext to agents over the trusted WebSocket sync channel. Agents never hold a key encryption key (KEK).
- **AAD owner binding**: each ciphertext is bound to its owner's user ID via AEAD additional-authenticated-data; rotating ownership is not possible without re-encryption.
- **API boundary**: both portal and admin APIs return only `key_last4` — request bodies never echo plaintext.
- **KEK source**: either `master.byok_kek` config (base64, 32 bytes) or — when omitted — derived via `HKDF-SHA256(jwt_secret, info="byok-kek-v1")`. See the production recommendation below.
- **Not covered in v1**: DNS rebinding; agent ↔ master WebSocket security depends on your deployment's TLS / network isolation.

### Production recommendation: bind a dedicated KEK

For anything beyond a dev / single-instance deployment, **set `master.byok_kek` explicitly** — generate it with `openssl rand -base64 32` and inject through environment variable, KMS, or a secret manager (avoid committing plaintext to yaml).

If you keep the HKDF fallback in production, you accept two coupled risks:

1. **Security coupling**: HKDF derives the KEK from `jwt_secret`. That single value then guards both token signatures *and* BYOK ciphertexts — leaking `jwt_secret` simultaneously leaks every stored BYOK key (an attacker who obtains it can recompute the derived KEK and decrypt the entire DB). Two high-sensitivity secrets become a single point of failure.
2. **Operational lock-in**: any `jwt_secret` rotation (routine, or response to an incident) invalidates every existing BYOK ciphertext. Users must re-enter their keys. With a dedicated `byok_kek`, you can rotate `jwt_secret` freely without disturbing BYOK data, and you can plan KEK rotation as its own independent process.

## Admin Configuration

### Enable / Disable BYOK

- Globally: `/system` → **BYOK Settings** → toggle **Globally enabled**.
- Per group: `/groups` → edit a group → **BYOK** card → leave "Use group-specific value" off to inherit the global switch, or turn it on and use the **BYOK enabled** switch to lock the group on/off.

### Quotas

- Global default max-channels-per-user: `/system` → **BYOK Settings** → adjust the number field.
- Per group override: edit a group → **BYOK** card → turn on "Use group-specific value" next to **Max private channels per user** and enter a number.

### Billing modes

- **Free** (default): BYOK requests do not deduct quota, but `usage_log` and stats are still recorded.
- **Service Fee**: BYOK costs are multiplied by `byok_service_fee_ratio` (default 0.1) and deducted from user quota. Configure ratio in `/system` → BYOK Settings.

### BaseURL allowlist

BYOK channel `base_url` field is restricted to a union of two lists:

- **System recommended**: built into the code as `consts.SystemBYOKBaseURLs` — updated each release. Admin cannot toggle these (if you need that, raise a separate issue).
- **Custom additions**: admin-editable list at `/system` → BYOK Settings → "Custom additions".

User-submitted `base_url` must `hasPrefix` match at least one entry in either list. Empty `base_url` defers to the provider type's built-in default URL.

### Admin cross-user view

Sidebar **BYOK** entry (under routing) opens `/admin/byok`, a read-only cross-user list of all private channels. Admin can:

- Filter by `owner_id` via `?owner_id=42` (also accessible from `/users` → row menu → "View BYOK Channels").
- Force-disable any channel (kill switch — sets status=0 + invalidates agent cache).

Admin **cannot** edit user keys or see plaintext.

## Troubleshooting

- **"base_url not in allowlist"**: admin needs to add this prefix to BYOK Settings → Custom additions.
- **"model not registered: gpt-X"**: gateway has no ModelConfig for that name. Admin needs to register the model first at `/models`.
- **"byok cipher not initialized"**: master misconfiguration — verify `master.byok_kek` (if set) decodes to 32 bytes, or that `master.jwt_secret` is non-empty.
- **Agent returns "decrypt key" error after key rotation**: master KEK was rotated; old ciphertexts no longer decryptable. Affected users need to re-enter their keys.
- **Channel not showing up in /v1/* requests**: confirm channel status=1 (enabled), models field includes the model name the request uses, and the user's group has BYOK enabled.

## Upgrading

New gateway versions may add provider URLs to `consts.SystemBYOKBaseURLs`. After upgrade, no admin action is required — the new system recommendations are auto-merged with admin custom additions at validation time.

See the "Production recommendation" subsection under **Security Model** for how to keep BYOK ciphertexts safe across `jwt_secret` rotations.
