# Explicit model routing members

Every member now stores `kind: "model" | "routing"` alongside `ref`, `priority`,
and `weight`. New create/update requests must include the kind. Preview requests
use the same representation. Master-to-agent synchronization preserves it.

```json
{
  "name": "deepseek-flash",
  "scope": "global",
  "members": [
    {"kind": "model", "ref": "deepseek-flash", "priority": 0, "weight": 1},
    {"kind": "model", "ref": "deepseek-v4.1-flash", "priority": 0, "weight": 1}
  ]
}
```

`model` is always a terminal model name, including when a same-name routing
exists. `routing` always references a global routing; missing/disabled routings
cannot fall back to a same-name model. Top-level request resolution retains the
existing token > user > global precedence.

The editor exposes models and routings as distinct choices, allows both kinds
with the same name, and excludes the current global routing from its routing
choices. User/token routings can still reference a different global routing with
the same name. `/v1/models?include_routings=false` supplies real model candidates
without routing-name shadowing, retaining the normal caller/whitelist checks.

## Validation and diagnostics

Only routing edges participate in graph traversal. Global create/update validates
the proposed graph, including ancestors, disabled routings, every priority group,
and all branches. A routing self-reference or `A -> B -> A` is rejected even when
a real model named A exists. Shared descendants (`A -> B -> D`, `A -> C -> D`)
are valid. Validation and mutation use one database transaction.

Runtime cycle and maximum depth guards remain as defense against invalid cached
or externally modified data. They reject invalid branches instead of changing
their member kind. Preview and marketplace use the same typed traversal; a
same-name real model is not a cycle, and a typed routing cycle remains a warning.
Retry exclusions distinguish `model:X` from `routing:X`.

## Upgrading existing installations

Back up the core database and coordinate the master and all agents on this
version before editing typed members. Older agents ignore `kind` and therefore
cannot implement the new semantics. Older API clients must be updated to send
the required member kind on create/update/preview.

Startup migrates legacy member JSON atomically and idempotently:

1. Existing explicit kinds remain unchanged.
2. A global routing's direct same-name reference becomes `model` when an enabled
   public channel supplies that model.
3. Other references to enabled global routings become `routing`.
4. Remaining legacy names become `model`, including names shadowed only by a
   disabled routing (the old runtime treated these as model leaves).

This freezes the effective legacy target type at migration time. Later additions,
renames, or enable/disable changes no longer reinterpret a model member as a route.

A legacy indirect cycle has path-dependent behavior: the old resolver could
terminate at a different same-name model depending on the entry point. One fixed
member type cannot preserve that meaning. Startup therefore reports the involved
routing IDs and rolls back **all member-kind updates**. Resolve these ambiguous
edges explicitly in the backed-up/offline core database before retrying the
upgrade; do not guess or automatically break the cycle. Existing stale model
references remain model references and retain ordinary availability diagnostics.
