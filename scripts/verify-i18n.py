#!/usr/bin/env python3
"""Verify every t("key") call referenced in .tsx has a translation in zh.json AND en.json.

Detects:
- `useTranslations("ns")` declares the namespace for subsequent t() calls in that file
- `t("key.subkey")` (and `tc(...)` / `tf(...)`) -- concatenated with active namespace(s)

A .tsx file may declare zero or more namespaces (multiple useTranslations calls).
Each t() call is checked against every namespace; if found in at least one, it's OK.
A file with NO namespace declaration treats t() calls as top-level keys.

Exit non-zero if any key is missing in zh.json or en.json (with a list).
"""
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "web" / "src"
LOCALE_DIR = ROOT / "web" / "src" / "i18n"
LOCALE_FILES = {
    "zh": LOCALE_DIR / "zh.json",
    "en": LOCALE_DIR / "en.json",
}

USE_TR_RE = re.compile(r'useTranslations\(\s*"([^"]+)"\s*\)')
T_CALL_RE = re.compile(r'\b(?:t|tc|tf)\(\s*"([^"]+)"')


def load_locale(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def flatten_keys(d: dict, prefix: str = "") -> set[str]:
    out: set[str] = set()
    for k, v in d.items():
        key = f"{prefix}.{k}" if prefix else k
        if isinstance(v, dict):
            out.update(flatten_keys(v, key))
        else:
            out.add(key)
    return out


def extract_required_keys(tsx_text: str) -> list[str]:
    """Return list of full keys (ns.subkey) used by t() in this file.

    If no useTranslations call exists, the t() arg is treated as a fully-qualified key.
    If multiple useTranslations calls exist, each t() arg is checked against all of them
    (we add all combinations; the validator counts a key satisfied if ANY combination exists).
    """
    namespaces = USE_TR_RE.findall(tsx_text)
    calls = T_CALL_RE.findall(tsx_text)
    if not calls:
        return []
    if not namespaces:
        return list(calls)
    # We don't know which call uses which namespace; emit each combination.
    out: list[str] = []
    for k in calls:
        for ns in namespaces:
            out.append(f"{ns}.{k}")
    return out


def main() -> int:
    locales: dict[str, set[str]] = {
        lang: flatten_keys(load_locale(path)) for lang, path in LOCALE_FILES.items()
    }

    missing: list[tuple[str, str, str]] = []  # (file, lang, key)
    for tsx in SRC.rglob("*.tsx"):
        text = tsx.read_text(encoding="utf-8")
        # If file has no useTranslations, the t() call's first dotted segment is the namespace.
        ns_list = USE_TR_RE.findall(text)
        calls = T_CALL_RE.findall(text)
        for k in calls:
            # Build candidate keys: each ns + "." + k, OR the bare k if it contains a dot.
            candidates: list[str] = []
            if ns_list:
                for ns in ns_list:
                    candidates.append(f"{ns}.{k}")
            if "." in k or not ns_list:
                candidates.append(k)
            for lang, keys in locales.items():
                if not any(c in keys for c in candidates):
                    missing.append((str(tsx.relative_to(ROOT)), lang, k))

    if missing:
        # De-duplicate
        seen = set()
        for f, lang, key in missing:
            tup = (f, lang, key)
            if tup in seen:
                continue
            seen.add(tup)
            print(f"missing key {key!r} in {lang} (used in {f})", file=sys.stderr)
        return 1
    print("i18n verify OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
