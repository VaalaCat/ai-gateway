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
