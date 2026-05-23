#!/usr/bin/env bash
set -euo pipefail

MODULE="github.com/VaalaCat/ai-gateway/internal/version"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
OUTPUT="${OUTPUT:-ai-gateway}"

LDFLAGS="-s -w"
LDFLAGS="${LDFLAGS} -X ${MODULE}.Version=${VERSION}"
LDFLAGS="${LDFLAGS} -X ${MODULE}.Commit=${COMMIT}"
LDFLAGS="${LDFLAGS} -X ${MODULE}.BuildDate=${BUILD_DATE}"

ensure_pnpm() {
  if command -v pnpm >/dev/null 2>&1; then
    return
  fi
  corepack enable
  corepack prepare pnpm@latest-10 --activate
}

if [[ -f "web/package.json" ]]; then
  # Run i18n verification (catches missing translation keys)
  echo "==> Verifying i18n keys..."
  python3 scripts/verify-i18n.py

  echo "Building web assets..."
  pushd web >/dev/null
  ensure_pnpm
  pnpm install --no-frozen-lockfile --lockfile=false
  pnpm build
  touch dist/.keep
  popd >/dev/null
fi

echo "Building ai-gateway..."
echo "  Version:    ${VERSION}"
echo "  Commit:     ${COMMIT}"
echo "  Build Date: ${BUILD_DATE}"
echo "  Output:     ${OUTPUT}"

go build -tags embed_assets -ldflags "${LDFLAGS}" -o "${OUTPUT}" ./cmd/

echo "Done: ./${OUTPUT}"
