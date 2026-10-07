---
name: "run-test"
description: "Launch, relaunch, or stop a local ci-chat-bot test instance with ngrok. Use when the user requests run-test or the local Slack bot workflow; not for unit tests."
---

# run-test

Launch, relaunch, or stop a local ci-chat-bot test instance with ngrok.
Invoke as `$run-test` in Codex or `/run-test` in Claude Code, or use when the user requests this workflow.

Before taking action, read [the workflow](references/workflow.md) with the file-reading tool and follow the section matching the user's requested action. Run shell commands from the repository root.

The reference contains the environment-file setup, launch, readiness, relaunch, and shutdown procedures. Read it as a file; shell and awk parameters in its code examples must retain their literal syntax.
