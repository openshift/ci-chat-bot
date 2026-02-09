# CLAUDE.md

## Project Overview

**ci-chat-bot** (also known as **Cluster Bot**) is a Slack App that enables users to launch and test OpenShift clusters from custom-built and existing releases. It runs in the Red Hat Internal Slack workspace and provides an interactive interface for cluster lifecycle management.

## Purpose

This bot simplifies the process of:
- Launching OpenShift clusters from various releases and custom PRs
- Testing multiple pull requests together
- Managing cluster lifecycle (creation, authentication, teardown)
- Running workflows with custom parameters
- Integrating with Prow CI/CD system for automated testing

## Architecture

### Technology Stack
- **Language**: Go 1.24
- **Slack SDK**: slack-go/slack
- **Kubernetes**: OpenShift client-go, k8s.io client libraries
- **CI/CD**: Prow (sigs.k8s.io/prow)
- **Issue Tracking**: Jira integration (go-jira)
- **Container Management**: OpenShift Hive, ROSA (Red Hat OpenShift Service on AWS)

### Key Components

#### 1. Core Packages

**pkg/manager/**
- [prow.go](pkg/manager/prow.go) - Prow job management for cluster launches
- [rosa.go](pkg/manager/rosa.go) - ROSA cluster management
- [mce.go](pkg/manager/mce.go) - Multi-Cluster Engine (MCE) integration
- [manager.go](pkg/manager/manager.go) - Main job orchestration logic
- [types.go](pkg/manager/types.go) - Common type definitions

**pkg/slack/**
- [events/](pkg/slack/events/) - Slack event handlers (messages, mentions, workflow submissions)
- [interactions/](pkg/slack/interactions/) - Interactive component handlers (modals, buttons)
- [parser/](pkg/slack/parser/) - Command parsing logic
- [modals/](pkg/slack/modals/) - Modal dialog registration and handling

**pkg/jira/**
- [jira.go](pkg/jira/jira.go) - Jira issue filing integration

**pkg/prow/**
- [prow.go](pkg/prow/prow.go) - Prow client and job execution

**pkg/catalog/**
- [catalog.go](pkg/catalog/catalog.go) - Operator catalog management
- [registry/](pkg/catalog/registry/) - Operator registry operations
- [operator/](pkg/catalog/operator/) - Operator handling utilities

#### 2. Entry Point
- [cmd/ci-chat-bot/slack.go](cmd/ci-chat-bot/slack.go) - HTTP server setup, event routing, health checks

### Architecture Flow

```
┌─────────────┐
│  Slack User │
└──────┬──────┘
       │ DM/Mention/Modal Interaction
       ▼
┌─────────────────────────────────────────────────────────────┐
│                         Slack API                            │
└──────┬──────────────────────────────────────────┬───────────┘
       │                                           │
       │ POST /slack/events-endpoint               │ POST /slack/interactive-endpoint
       │ (Messages, Mentions, Workflows)           │ (Modals, Buttons, Shortcuts)
       ▼                                           ▼
┌──────────────────────────────────────────────────────────────────────┐
│                    ci-chat-bot HTTP Server                           │
│  Endpoints: /, /readyz, /slack/events-endpoint,                     │
│             /slack/interactive-endpoint                              │
└──────┬──────────────────────────────────────────┬────────────────────┘
       │                                           │
       │ Signature Verification                    │ Signature Verification
       ▼                                           ▼
┌─────────────────────────┐              ┌─────────────────────────────┐
│   Event Router          │              │   Interaction Router         │
│   (MultiHandler Chain)  │              │   (Modal Registry)           │
└──┬───┬───┬───┬──────────┘              └─────────┬───────────────────┘
   │   │   │   │                                    │
   │   │   │   │ AppHomeHandler                     │ ViewSubmission
   │   │   │   └─────────┐                          │ BlockActions
   │   │   │             │                          │ Shortcuts
   │   │   │ WorkflowSubmissionHandler              │ WorkflowStepEdit
   │   │   └──────┐      │                          │
   │   │          │      │                          │
   │   │ MentionHandler  │                          │
   │   └────┐     │      │                          │
   │        │     │      │                          │
   │ MessagesHandler      │                          │
   │        │     │      │                          │
   │        ▼     ▼      ▼                          ▼
│        ┌────────────────────────────────────────────────────┐
│        │           Command Parser / Modal Handler           │
│        │  - Matches command regex patterns                  │
│        │  - Routes modal flows (20+ registered)             │
│        │  - Extracts parameters from user input             │
│        └──────────────────┬─────────────────────────────────┘
│                           │
│                           ▼
│                  ┌──────────────────┐
│                  │   Job Manager    │
│                  │  - LaunchJobForUser()
│                  │  - ROSAClusterForUser()
│                  │  - MCEClusterForUser()
│                  └─┬────┬──────┬────┘
│                    │    │      │
│     ┌──────────────┘    │      └──────────────┐
│     │                   │                     │
│     ▼                   ▼                     ▼
│  ┌──────────────┐  ┌────────────────┐  ┌──────────────────┐
│  │  Prow Jobs   │  │   ROSA API     │  │ MCE Integration  │
│  │              │  │   (OCM Client) │  │ (Hive/ACM)       │
│  │ - ProwJob CR │  │ - Create       │  │ - ClusterDeployment
│  │ - Workflows  │  │ - Monitor      │  │ - ManagedCluster │
│  │ - Builds     │  │ - Add Admin    │  │ - Namespace      │
│  │ - Tests      │  │                │  │ - Secrets        │
│  └──────┬───────┘  └────────┬───────┘  └─────────┬────────┘
│         │                   │                     │
│         │ Kubernetes API    │ OCM API             │ Kubernetes API
│         ▼                   ▼                     ▼
│  ┌─────────────────┐ ┌──────────────────┐ ┌─────────────────────┐
│  │ OpenShift CI    │ │ AWS ROSA         │ │ Hive Provisioned    │
│  │ Clusters        │ │ Clusters         │ │ Clusters            │
│  │ (All platforms) │ │ (Managed)        │ │ (AWS/GCP)           │
│  └─────────┬───────┘ └──────┬───────────┘ └──────┬──────────────┘
│            │                │                     │
│            │ Credentials    │ Credentials         │ Credentials
│            └────────┬───────┴──────┬──────────────┘
│                     │              │
│                     ▼              ▼
│              ┌─────────────────────────────┐
│              │   Background Sync Loops     │
│              │  - Prow Job Monitor         │
│              │  - ROSA Cluster Monitor     │
│              │  - MCE Provision Monitor    │
│              └──────────┬──────────────────┘
│                         │
│                         ▼
│              ┌─────────────────────────────┐
│              │   Notifier Callbacks        │
│              │  - JobResponder (Prow)      │
│              │  - RosaResponder (ROSA)     │
│              │  - MceResponder (MCE)       │
│              └──────────┬──────────────────┘
│                         │
│                         │ PostMessage/UploadFile
│                         ▼
│              ┌─────────────────────────────┐
│              │       Slack API             │
│              │  - Status updates           │
│              │  - Kubeconfig files         │
│              │  - Error messages           │
│              │  - Console URLs             │
│              └──────────┬──────────────────┘
│                         │
│                         ▼
                   ┌─────────────┐
                   │  Slack User │
                   └─────────────┘
```

**Key Flow Patterns:**

1. **HTTP Endpoints**: Two main endpoints handle all Slack communication
   - `/slack/events-endpoint` - DM messages, mentions, workflow steps, app home
   - `/slack/interactive-endpoint` - Modals, buttons, shortcuts

2. **Event Processing**: MultiHandler chain with 4 handlers
   - MessagesHandler: Text commands in DMs (uses command parser)
   - MentionHandler: @mentions in channels (triggers interactive buttons)
   - WorkflowSubmissionHandler: Slack Workflow Builder integrations (Jira)
   - AppHomeHandler: Dynamic app home view with cluster status

3. **Interaction Processing**: Modal registry with 20+ flows
   - Launch workflow (7 steps), MCE workflow (7 steps)
   - Cluster management (list, auth, done, refresh)
   - Each modal stores state in PrivateMetadata JSON

4. **Backend Strategies**: Three parallel cluster management systems
   - **Prow**: CI-based cluster launches (all platforms via ProwJob CRs)
   - **ROSA**: Direct OCM API calls (managed AWS clusters)
   - **MCE**: Hive ClusterDeployment CRs (multi-cluster engine)

5. **Async Monitoring**: Background sync loops watch cluster state
   - Poll every minute for status changes
   - Extract credentials when available
   - Trigger notifier callbacks

6. **Response Flow**: Notifiers post back to Slack
   - Success: Kubeconfig upload + console URL + oc login command
   - Failure: Error message + logs link
   - Progress: Status updates with live links

## Key Features

### 1. Cluster Launch Commands
- **Basic launch**: Launch clusters from specific versions or PRs
- **Multi-PR launches**: Combine multiple PRs from different repos
- **Platform support**: AWS, GCP, Azure, Metal, and more
- **workflow-launch**: Simplified workflow execution with custom parameters
- **workflow-test**: Run workflows while preserving test stages

### 2. Cluster Management
- **auth**: Retrieve cluster credentials (kubeconfig)
- **list**: Show active clusters for a user
- **done**: Tear down a cluster
- **refresh**: Extend cluster lifetime

### 3. Workflow Parameters
Workflows support complex parameter configurations:
- Environment variables
- Nested parameters (semicolon-separated)
- Capability sets
- Platform-specific settings

Example:
```
workflow-launch openshift-e2e-gcp 4.19 "BASELINE_CAPABILITY_SET=None","ADDITIONAL_ENABLED_CAPABILITIES=CloudControllerManager CloudCredential Console Ingress MachineAPI"
```

### 4. Platform-Specific Features

**Metal/Bare-Metal Clusters**:
- Require proxy access (httpProxy/httpsProxy)
- Provide proxy-url in kubeconfig
- See [FAQ.md](docs/FAQ.md) for access details

**ROSA Clusters**:
- AWS-based managed OpenShift
- Special handling in [pkg/manager/rosa.go](pkg/manager/rosa.go)

## Development Guidelines

### Code Structure
1. **Event Handlers**: Located in `pkg/slack/events/`, handle incoming Slack events
2. **Interaction Handlers**: Located in `pkg/slack/interactions/`, handle user interactions with modals/buttons
3. **Job Managers**: Located in `pkg/manager/`, orchestrate cluster lifecycle
4. **Command Parsers**: Located in `pkg/slack/parser/`, parse user commands

### Adding New Features

**To add a new workflow**:
1. Edit [workflows-config.yaml](https://github.com/openshift/release/blob/master/core-services/ci-chat-bot/workflows-config.yaml) in openshift/release
2. Specify platform and any required base_images
3. Submit PR to openshift/release

**To add a new command**:
1. Update parser in `pkg/slack/parser/`
2. Add handler in appropriate event/interaction handler
3. Update help text in bot's supported commands

### Testing
- Unit tests: `*_test.go` files (using Ginkgo/Gomega)
- Test command: `make test` (includes race detection via `-race` flag)
- Manual test command: `go test ./...` or `go test -race ./...` for race detection
- Race detection is enabled by default in the Makefile to catch concurrency issues
- Integration tests in `pkg/manager/manager_test.go` and `pkg/manager/prow_test.go`

**Important**: The project includes concurrent operations (e.g., GCP access management with mutexes), so tests should always run with the race detector enabled to ensure thread safety.

### Pre-commit Verification
Before committing changes, run the following commands to ensure code quality:

```bash
make verify  # Run verification checks (govet, gofmt, etc.)
make lint    # Run golangci-lint for code quality checks
make test    # Run all tests with race detection
make all     # Build all binaries
```

Or run all checks at once:
```bash
make verify lint test all
```

This ensures that:
- Code passes static analysis and formatting checks
- All tests pass with race detection enabled
- The project builds successfully
- No regressions are introduced

## Configuration

### Required Environment Variables
- `BOT_TOKEN`: Slack bot token
- `BOT_SIGNING_SECRET`: Slack signing secret for request verification
- Prow configuration for job execution
- Optional: Jira credentials for issue filing

### Deployment
- Runs as an HTTP server listening on configured port
- Health checks at `/readyz` endpoint
- Metrics exposed via Prometheus
- Graceful shutdown handling via interrupts package

## Common Workflows

### User Perspective
1. User sends command to @cluster-bot in Slack
2. Bot parses command and validates parameters
3. Bot creates Prow job or directly manages cluster (ROSA/MCE)
4. Bot notifies user of progress/completion
5. User retrieves credentials with `auth` command
6. User interacts with cluster
7. User tears down with `done` command or cluster auto-expires

### Developer Perspective
1. Slack event arrives at `/slack/events-endpoint`
2. Event verified and parsed
3. Router dispatches to appropriate handler
4. Handler uses JobManager to execute action
5. JobManager creates Prow job or manages cluster directly
6. Notifier sends updates back to Slack user

## Important Files

- [README.md](README.md) - User-facing documentation
- [docs/FAQ.md](docs/FAQ.md) - Frequently asked questions
- [go.mod](go.mod) - Go module dependencies
- [cmd/ci-chat-bot/slack.go](cmd/ci-chat-bot/slack.go) - Main HTTP server
- `pkg/manager/` - Core business logic
- `pkg/slack/` - Slack integration
- `pkg/prow/` - Prow job management

## Resources

- **OpenShift Releases**: https://amd64.ocp.releases.ci.openshift.org/
- **Support Channel**: #forum-ocp-crt (Red Hat Internal Slack)
- **Release Repository**: https://github.com/openshift/release
- **CI Documentation**: https://docs.ci.openshift.org/

## Notes for Claude

- This is a production service used by OpenShift developers
- Changes should be thoroughly tested before deployment
- Many workflows are defined in openshift/release repository, not this repo
- Cluster lifetimes are limited and automatically cleaned up
- Metal/bare-metal clusters require special proxy configuration
- The bot integrates deeply with Red Hat's Prow CI infrastructure

### Testing and Verification Guidelines for Claude

**When to run verification commands:**

After making any code changes (especially to `.go` files), you should proactively run:
```bash
make verify lint test all
```

This is REQUIRED for:
- Adding or modifying Go code in `pkg/`, `cmd/`, or any package
- Changing test files (`*_test.go`)
- Modifying the Makefile or build configuration
- Adding new dependencies or updating `go.mod`

This is OPTIONAL but RECOMMENDED for:
- Documentation-only changes (`.md` files)
- Configuration file changes (`.yaml`, `.json`)

**How to handle failures:**
- If `make verify` fails: Fix formatting/vet issues before proceeding
- If `make lint` fails: Address linting issues or document why they can be ignored
- If `make test` fails: Fix the failing tests or update them if behavior changed intentionally
- If `make all` fails: Fix build errors before suggesting the changes are complete

**Race detection note:**
The `-race` flag is enabled by default in `GO_TEST_FLAGS`. Any failures related to race conditions MUST be fixed, as this indicates actual concurrency bugs in production code (especially in GCP access management, Slack handlers, and other concurrent operations).

**Always inform the user:**
After running verification commands, inform the user of the results:
- ✅ "All verification checks passed: verify, lint, test, and build successful"
- ⚠️ "Verification passed with warnings: [describe warnings]"
- ❌ "Verification failed: [describe failures and fixes needed]"
