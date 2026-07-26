#!/usr/bin/env bash

set -euo pipefail

readonly go_package_parallelism=2
readonly go_max_procs=2
readonly go_test_timeout=300s

run_frontend_install() {
	pnpm --dir web install --frozen-lockfile
}

run_frontend_lint() {
	pnpm --dir web lint
}

run_frontend_unit_tests() {
	pnpm --dir web test:unit
}

run_frontend_build() {
	pnpm --dir web build
}

run_go_tests() {
	CGO_ENABLED=0 GOMAXPROCS="${go_max_procs}" go test ./... \
		-p "${go_package_parallelism}" -count=1 -timeout="${go_test_timeout}"
}

run_agent_tunnel_race_tests() {
	CGO_ENABLED=1 GOMAXPROCS="${go_max_procs}" go test -race \
		./internal/pkg/tunnel/... \
		./internal/pkg/attemptproxy/... \
		./internal/pkg/agentproxy/... \
		./internal/agent/tunnel/... \
		./internal/agent/attemptproxy/... \
		./internal/agent/relay/pipeline/exec/... \
		./internal/master/tunnel/... \
		./internal/master/connectivity/... \
		./internal/agent/body/... \
		-p "${go_package_parallelism}" -count=1 -timeout="${go_test_timeout}"
}

run_agent_relay_race_tests() {
	CGO_ENABLED=1 GOMAXPROCS="${go_max_procs}" go test -race ./internal/master \
		-run 'AgentRelayRollout|DirectTunnel|TransportPolicy' \
		-p "${go_package_parallelism}" -count=1 -timeout="${go_test_timeout}"
}

run_all() {
	run_frontend_install
	run_frontend_lint
	run_frontend_unit_tests
	run_frontend_build
	run_go_tests
	run_agent_tunnel_race_tests
	run_agent_relay_race_tests
}

case "${1:-all}" in
	all) run_all ;;
	frontend-install) run_frontend_install ;;
	frontend-lint) run_frontend_lint ;;
	frontend-unit-tests) run_frontend_unit_tests ;;
	frontend-build) run_frontend_build ;;
	go-tests) run_go_tests ;;
	agent-tunnel-race-tests) run_agent_tunnel_race_tests ;;
	agent-relay-race-tests) run_agent_relay_race_tests ;;
	*)
		echo "unknown CI phase: $1" >&2
		exit 2
		;;
esac
