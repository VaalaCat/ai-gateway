# Contributing to AI Gateway

Thank you for considering contributing to AI Gateway! This guide will help you get started.

## Development Setup

### Prerequisites

- Go 1.25+
- Node.js 20+
- pnpm (latest)

### Local Build

```bash
# Build frontend + backend (single binary)
CGO_ENABLED=0 bash ./build.sh

# Or build frontend and backend separately:
cd web && pnpm install && pnpm build && cd ..
CGO_ENABLED=0 go build -o ai-gateway ./cmd/
```

### Running Locally

```bash
cp config.example.yaml config.yaml
# Edit config.yaml with your settings
./ai-gateway master --config config.yaml
```

### Frontend Development

```bash
cd web
pnpm install
pnpm dev
```

The dev server runs on port 8141 and proxies API requests to `localhost:8140`.

## Code Style

- **Go:** Standard `gofmt` formatting. Run `go vet ./...` before committing.
- **Frontend:** ESLint configuration is included. Run `pnpm lint` in the `web/` directory.
- **Announcements:** Load after auth hydration and fetch the user's feed when signing in from guest mode; cache each identity separately for the page lifetime. Navigation, token renewal, focus, and reconnect do not refetch. The inbox uses shared pagination without a page-size selector; management uses the standard table footer. Keep the last successful page and total during page requests. A 150 ms hover/focus dwell preloads details or the next page into a separate, identity-scoped five-second cache; only navigation publishes page summaries, and opening consumes a prefetched detail once. Hover never acknowledges reading. Short requests do not flash a loading indicator; show it only after 150 ms. For loaded `show_for_guests` announcements only, synchronize exact `id:revision` browser receipts with account receipts in both directions; private announcements never import/export browser receipts. Reannouncement revisions still alert. Malformed receipt JSON is replaced on the next successful save; storage/server failures remain explicitly retryable without request loops. Create/update/delete must run outside an existing transaction; cache publication requires their own commit.
- **Public configuration hydration:** Restore browser branding after provider hydration and keep each consumer's server snapshot until its own hydration completes. A nested Suspense boundary may hydrate after the provider restores cached configuration; test that boundary, not just an immediately hydrated child.

## Pull Request Process

1. Fork the repository
2. Create a feature branch (`git checkout -b feat/my-feature`)
3. Make your changes with clear commit messages
4. Ensure all tests pass: `CGO_ENABLED=0 go test ./... -count=1 -timeout=120s`
5. Push and open a Pull Request against `main`

## Testing

```bash
# Run all Go tests
CGO_ENABLED=0 go test ./... -count=1 -timeout=120s

# Run a specific package's tests
CGO_ENABLED=0 go test ./internal/agent/relay/... -v
```

## License

By contributing to AI Gateway, you agree that your contributions will be licensed under the MIT License.
