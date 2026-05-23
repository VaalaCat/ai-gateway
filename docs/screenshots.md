# Screenshots

A walkthrough of every page in the Web UI. All screenshots are taken with seeded demo data — every `demo-*` name (channels, tokens, users) is a placeholder, not real production data.

> 中文版本: [screenshots.zh.md](screenshots.zh.md)

---

## Overview

### Dashboard

At-a-glance counters for users, tokens, channels, models, agents, and cumulative cost.

![Dashboard](images/en/dashboard.png)

---

## Identity & Access

### Users

Manage admin and end-user accounts; assign quotas and group membership.

![Users](images/en/users.png)

### User Groups

Group-level allow-lists for channels and models. New users inherit their group's policy.

![User Groups](images/en/groups.png)

### Token Templates

Reusable presets for creating API keys with consistent model lists and expiration policies.

![Token Templates](images/en/token-templates.png)

### OAuth Providers

Register OIDC / OAuth2 identity providers (GitHub, Google, custom IdPs) for SSO login.

![OAuth Providers](images/en/oauth-providers.png)

### Profile

End-user profile, quota usage, and OAuth identity bindings.

![Profile](images/en/profile.png)

---

## Channels & Models

### Channels

Configure upstream AI service providers. 50+ providers supported (OpenAI, Anthropic, Gemini, DeepSeek, Ollama, …) with weight/priority load balancing.

![Channels](images/en/channels.png)

### Models

Per-model pricing configuration (input / output / cache tiers).

![Models](images/en/models.png)

### Agents

Data-plane worker nodes — either a single embedded agent or multiple distributed agents enrolled via token.

![Agents](images/en/agents.png)

### Agent Routes

Pin specific channels or routings to specific agents (e.g. EU traffic only on EU-region agents).

![Agent Routes](images/en/agent-routes.png)

### Model Routings

Aggregate multiple upstream channel-models under one virtual model name, with priority and weighted load balancing.

![Model Routings](images/en/model-routings.png)

---

## BYOK (Bring Your Own Key)

End users plug in their own provider API key — cost is billed directly against their own account and does not consume gateway quota.

### BYOK Channels

Per-user list of private channels: status, allowed models, load-balancing weight, and a connectivity-test action.

![BYOK Channels](images/en/byok.png)

### New BYOK Channel

Stepped form: Basics → Endpoints & Protocol → Models → Protocol Behavior → Request Rewrite → Routes / Roles. Models must be a subset of the gateway's registered model list.

![New BYOK Channel](images/en/byok-new.png)

### Edit BYOK Channel

Modify name, Base URL, status, protocol behavior, etc. API key echoes only the last 4 digits — leave blank to keep the existing key.

![Edit BYOK Channel](images/en/byok-edit.png)

### BYOK Usage Stats

Per-user request / token / cost trend across their own private channels, broken down by channel and by model.

![BYOK Usage](images/en/byok-stats.png)

### All BYOK Channels (Admin)

Cross-user audit view of every private channel + owner + status. Plaintext API keys are never shown; admin can disable any channel with one click.

![All BYOK Channels](images/en/admin-byok.png)

---

## Tokens & Usage

### Tokens

Per-user API keys with optional model allow-list, channel allow-list, and expiration.

![Tokens](images/en/tokens.png)

### Logs

Per-request audit log with token / user / channel / model / cost / duration / status, plus drill-down to the raw request/response trace.

![Usage Logs](images/en/logs.png)

### Billing

Daily rollups by token and by channel — total cost, request count, success rate, token usage. Rebuild from raw logs on demand.

![Billing](images/en/billing.png)

---

## Tools

### Playground

In-browser chat tester for any configured model. Supports Chat / JSON / SSE views and arbitrary system prompts.

![Playground](images/en/playground.png)

### My Routings

User-scoped model routings — each user can define their own private pools without touching global routings.

![My Routings](images/en/profile-model-routings.png)

---

## Operations

### Monitoring

Cluster health overview: success rate, agents online, TPS, request count; per-channel and per-agent 24h trend and error rate; errors broken down by request stage.

![Monitoring](images/en/monitoring.png)

### Entity Insight

Drill-down view for a single entity (agent / channel / model / token): KPIs, trend, errors, stage-latency distribution, related breakdowns.

![Entity Insight](images/en/monitoring-insight.png)

### System Settings

Site-wide settings: registration toggle, branding, feature flags.

![System Settings](images/en/system.png)

### Cache Monitoring

LRU cache stats for the agent's token/user cache — hit rate, capacity, eviction count.

![Cache Monitoring](images/en/monitoring-cache.png)

---

## Authentication

### Login

Username + password and OAuth login via configured providers.

![Login](images/en/login.png)

### Register

Self-registration (can be toggled off in System Settings).

![Register](images/en/register.png)
