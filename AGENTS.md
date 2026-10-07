# AGENTS.md

ci-chat-bot (Cluster Bot) is a production Slack app for launching, testing, and
managing OpenShift clusters through Prow, ROSA, and MCE/Hive.
Keep responses concise.

## Repository map

Requests flow from Slack HTTP endpoints through event or interaction handlers to
`pkg/manager/`; background monitors send status and credentials back to Slack.

- `cmd/ci-chat-bot/`: startup, configuration, and HTTP endpoints.
- `pkg/slack/slack.go`: command registration and bot setup.
- `pkg/slack/events/`, `pkg/slack/interactions/`: event and interaction routing.
- `pkg/slack/parser/`: command parsing.
- `pkg/slack/modals/`: modal registration, views, and handlers; state is stored in `PrivateMetadata`.
- `pkg/manager/`: orchestration and monitoring in `manager.go`; backend operations in `prow.go`, `rosa.go`, and `mce.go`; GCP access management in `gcp_access.go`.
- `pkg/prow/`, `pkg/catalog/`: Prow job helpers and operator catalog utilities.
- [README.md](README.md) and [docs/FAQ.md](docs/FAQ.md): user documentation, including metal cluster proxy requirements.

## Making changes

- For new commands, update registration in `pkg/slack/slack.go`, the relevant
  handlers, and help text. Change `pkg/slack/parser/` when parsing behavior changes.
- Preserve cluster lifetime limits and automatic cleanup when changing lifecycle
  handling. Account for concurrent access in managers, monitors, and Slack handlers.

## Build and verification

Use the Go version specified in [go.mod](go.mod).
The `gcs` build tag is required for the vendored `cyborg-data` GCS client.
The Makefile sets it for build, test, and vet; `.golangci.yml` sets it for lint.
`.codex/config.toml` and `.claude/settings.json` configure `GOFLAGS=-tags=gcs`
for their respective tools. For direct Go commands outside those environments,
set the flag explicitly.

After changing Go code (including tests), build configuration, or dependencies,
and before committing those changes, run:

```bash
make verify lint test all
```

This runs formatting/vet checks, lint, tests with race detection, and builds.
Always enable race detection for tests; for example, a focused test run is:

```bash
GOFLAGS=-tags=gcs go test -race ./pkg/slack/parser/...
```

Fix formatting, vet, test, race, and build failures before declaring completion.
Resolve lint findings or explain why they can be ignored. Report verification
results and any checks that could not run. Full verification is optional for
documentation-only or non-build YAML/JSON changes.

## Local test instance

When asked to launch, relaunch, or stop a local bot with ngrok, use the `run-test`
skill and follow [.agents/skills/run-test/SKILL.md](.agents/skills/run-test/SKILL.md).
