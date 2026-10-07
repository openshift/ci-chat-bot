# Local Test Instance Workflow

You are helping the user run a test instance of the ci-chat-bot. Follow these steps:

## Security Warning

**IMPORTANT - Security Notice**: This command will ask you to provide Slack credentials during setup.

- Do NOT share the chat transcript or logs containing these credentials with others
- Credentials will be visible in process listings (`ps aux`) while the bot is running
- The ngrok tunnel exposes your local bot instance to the internet - only use test/development Slack apps
- Logs at `/tmp/ci-chat-bot/bot.log` may contain sensitive information
- For production deployments, use proper secret management (Kubernetes secrets, vault, etc.) instead of environment variables
- **Process management**: this workflow tracks the bot session and ngrok process via PID files (`/tmp/ci-chat-bot/bot.pid`, `/tmp/ci-chat-bot/ngrok.pid`). The bot runs in its own session/process group so `make run` and its child processes can be stopped together. **Never** use broad-match kill commands (`pkill -f <generic substring>`, `killall`, `pkill node`, `pkill go`, `pkill ngrok`, etc.) in this workflow — a broad pattern can match unrelated processes, including the coding agent's own process tree, and kill it.

0. **Prepare the working directory**: Set a restrictive umask before creating any files and ensure the directory is owned by you with owner-only permissions, even if it already exists:
   ```bash
   umask 077
   if [ -L /tmp/ci-chat-bot ] || ! mkdir -p -- /tmp/ci-chat-bot ||
      [ ! -O /tmp/ci-chat-bot ] || ! chmod 700 -- /tmp/ci-chat-bot; then
     echo "Cannot secure /tmp/ci-chat-bot; stop and inspect it manually."
     exit 1
   fi
   ```
   Use `umask 077` in every shell that creates workflow files. All logs and PID files for this workflow live under this one directory rather than scattered directly in `/tmp`.

1. **Check Environment Variables**: First ask the user if they want to load environment variables from a file.

   **Option A: Load from Environment File (Recommended)**

   Ask the user for the path to their environment file (e.g., `.env`, `.env.local`, etc.).

   The file should contain one variable per line in the format:
   ```bash
   BOT_TOKEN=xoxb-your-token-here
   BOT_SIGNING_SECRET=your-signing-secret
   GITHUB_TOKEN=ghp_your-github-token
   GCP_ACCESS_DRY_RUN=true
   GCP_SERVICE_ACCOUNT_JSON={"type":"service_account",...}
   ORG_DATA_BUCKET=your-org-data-bucket
   ```

   If the user provides a file path:
   - Verify the file exists
   - Load the environment variables using `source` or `export $(cat file | xargs)`
   - Store the file path to use in step 5

   **Option B: Manual Entry (if no env file)**

   If they do NOT have an env file or prefer manual entry, ask the user to provide:
   - `BOT_TOKEN`: Slack Bot Token (required) - starts with `xoxb-`
   - `BOT_SIGNING_SECRET`: Slack App Signing Secret (required)
   - `GITHUB_TOKEN`: GitHub token (optional but recommended)
   - `GCP_ACCESS_DRY_RUN`: Set to `true` to enable dry-run mode for GCP credentials (optional)
   - `GCP_SERVICE_ACCOUNT_JSON`: GCP service account JSON for credentials command (optional)
   - `ORG_DATA_BUCKET`: GCS bucket for organizational data (optional)

   Store these values to use in step 5. Tell the user where to find these values:
   - Go to https://api.slack.com/apps
   - Select their app
   - **BOT_TOKEN**: OAuth & Permissions → Bot User OAuth Token
   - **BOT_SIGNING_SECRET**: Basic Information → App Credentials → Signing Secret

   **About GCP_ACCESS_DRY_RUN**:
   - When set to `true`, the bot will skip all IAM policy changes for the `credentials` command
   - BigQuery audit logging will still work normally
   - Useful for testing the credentials command without affecting production IAM
   - Safe to use even if you're already a project owner
   - See TESTING_DRY_RUN.md for more details

2. **Verify Cluster Access**: Confirm the user has `oc` CLI access to the `app.ci` cluster context:
   - Run `oc --context app.ci whoami` to verify access
   - If this fails, the user needs to authenticate to the OpenShift CI cluster first

3. **Setup ngrok Tunnel**: Start ngrok to expose the bot to Slack:
   - Run ngrok in the background, capturing its output and PID so it can be managed later:
     ```bash
     ngrok http 8080 > /tmp/ci-chat-bot/ngrok.log 2>&1 &
     echo $! > /tmp/ci-chat-bot/ngrok.pid
     ```
   - Extract and display the public HTTPS URL that ngrok provides
   - The URL will look like: `https://xxxx-xx-xx-xx-xx.ngrok-free.app`
   - Inform the user they need to configure this URL in their Slack app settings:
     - Go to the Slack app configuration page (https://api.slack.com/apps)
     - Navigate to "Interactivity & Shortcuts"
       - Set Request URL to: `https://xxxx-xx-xx-xx-xx.ngrok-free.app/slack/interactive-endpoint`
     - Navigate to "Event Subscriptions"
       - Set Request URL to: `https://xxxx-xx-xx-xx-xx.ngrok-free.app/slack/events-endpoint`

4. **Build the Project**: Run `make` to build the ci-chat-bot binary.

5. **Run the Full Setup**: Execute the complete setup with log redirection.

   Before any launch or retry, check the recorded bot session and listeners on ports 8080 (Slack HTTP), 8081 (health), and 9090 (metrics): `ss -ltnp '( sport = :8080 or sport = :8081 or sport = :9090 )'`. If a bot session is still live, continue verifying it or use the relaunch procedure below; never start a second instance. If any of these ports is occupied by an unidentified process, investigate its owner before launching. Preserve the existing PID file and logs until the previous session has been accounted for.

   In an agent tool environment, use a persistent managed command session if background processes do not reliably survive tool completion. Run the launch command below with `setsid --wait` and without the trailing `&`, and retain the tool's session handle. `--wait` keeps the launcher attached when `setsid` forks because it is a process-group leader. The tool handle is separate from the OS session ID in `bot.pid`. `nohup` alone does not guarantee survival when the tool runner cleans up processes. Use the same persistent-session approach for ngrok when needed, recording its actual process PID. A tool timeout or wrapper exit does not prove its child processes exited.

   **If using an environment file (Option A from step 1):**
   ```bash
   setsid bash -c 'set -e; set -a; source "$1"; set +a; printf "%s\n" "$$" > "$2"; exec make run' _ \
     "/path/to/.env" /tmp/ci-chat-bot/bot.pid > /tmp/ci-chat-bot/bot.log 2>&1 &
   ```
   Replace `/path/to/.env` with the actual file path provided by the user.

   **If using manual entry (Option B from step 1):**

   Normal mode (with IAM changes):
   ```bash
   setsid env BOT_TOKEN='<token-from-step-1>' BOT_SIGNING_SECRET='<secret-from-step-1>' \
     bash -c 'printf "%s\n" "$$" > "$1"; exec make run' _ /tmp/ci-chat-bot/bot.pid \
     > /tmp/ci-chat-bot/bot.log 2>&1 &
   ```

   Dry-run mode (recommended for testing credentials command):
   ```bash
   setsid env GCP_ACCESS_DRY_RUN=true BOT_TOKEN='<token-from-step-1>' BOT_SIGNING_SECRET='<secret-from-step-1>' \
     bash -c 'printf "%s\n" "$$" > "$1"; exec make run' _ /tmp/ci-chat-bot/bot.pid \
     > /tmp/ci-chat-bot/bot.log 2>&1 &
   ```

   Use the actual values provided by the user in step 1.

   This will:
   - Extract kubeconfig files from the `ci-chat-bot-kubeconfigs` secret
   - Get Boskos credentials from the `boskos-credentials` secret
   - Extract ROSA configuration (subnet IDs, OIDC config ID, billing account ID)
   - Extract MCE kubeconfig and token
   - Build the binary if needed
   - Start the bot with all required configuration
   - Redirect all output to `/tmp/ci-chat-bot/bot.log` for easy monitoring
   - Record the isolated bot session's ID to `/tmp/ci-chat-bot/bot.pid`

   The wrapper writes its own PID from inside the new session before it replaces itself with `make run`. That PID is the session ID and process-group ID shared by `make run`, `hack/run.sh`, and the bot child. Do not record `$!`: `setsid` can fork when launched from an interactive shell, making `$!` refer to the wrong process.

6. **Verify the Bot is Running**:
   - **Check the whole session, not just the wrapper PID.** The session leader (`make run`) can exit while the bot child remains alive. Validate the recorded ID, then check for non-zombie processes with both the recorded process-group ID and session ID:
     ```bash
     BOT_SESSION=$(cat /tmp/ci-chat-bot/bot.pid)
     if [[ ! "$BOT_SESSION" =~ ^[1-9][0-9]*$ ]]; then
       echo "Invalid bot session ID; inspect manually before proceeding."
       exit 1
     fi
     ps -eo pgid=,sid=,stat= | awk -v sid="$BOT_SESSION" '
       $1 == sid && $2 == sid && $3 !~ /^Z/ { found=1 }
       END { exit !found }
     '
     ```
     Exit status 0 means the session is still live, even if `ps -p "$BOT_SESSION"` finds no process. Inspect session members using `ps -eo pid=,pgid=,sid=,stat=,comm=`; avoid printing credential-bearing command lines.
   - **Allow initialization to finish.** Secret extraction, building, organizational data loading, and cache synchronization can delay the port 8080 listener. `Waiting for caches to sync` indicates initialization, not failure. Poll session liveness and HTTP readiness every 5 seconds for up to 120 seconds, using tool waits of at most 30 seconds so progress can be reported. A refused connection during this period does not justify a retry.
   - Check `curl --max-time 5 -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/` and confirm that the listener belongs to the bot in the recorded session. Port 8081 is the health listener and can appear before port 8080; its presence alone does not mean Slack serving is ready. Metrics use port 9090 by default.
   - If the deadline expires while the session is live, report that initialization is still pending and inspect recent sanitized logs. Do not declare the process dead or launch a duplicate. If no live session members remain, inspect the logs and identify the exit cause before retrying. An `address already in use` error requires checking port ownership and resolving the conflict through the narrow relaunch procedure, rather than starting another instance.
   - Verify ngrok separately through `http://127.0.0.1:4040/api/tunnels`, then check HTTP readiness through its public URL. A temporarily unavailable ngrok API is not evidence that the bot failed. Check ngrok's tracked process and logs before restarting the tunnel.
   - Report success once local and tunneled HTTP checks pass. Ask the user to send a Slack message to verify end-to-end connectivity; HTTP readiness alone does not verify Slack app configuration.

7. **Inform User About Log Monitoring**: After starting the bot, inform the user:
   - Logs are saved to `/tmp/ci-chat-bot/bot.log`
   - They can monitor logs in real-time with: `tail -f /tmp/ci-chat-bot/bot.log`
   - To filter for errors: `tail -f /tmp/ci-chat-bot/bot.log | grep -i error`
   - To filter for warnings: `tail -f /tmp/ci-chat-bot/bot.log | grep -i warning`

## Relaunching the Bot

"Relaunching" means restarting **only** the ci-chat-bot process. **Leave ngrok running** — its tunnel URL doesn't need to change across a relaunch, and the Slack app's configured Request URLs point at that tunnel, so tearing it down would break the Slack app config for no reason. The `/tmp/ci-chat-bot/ngrok.pid` file exists solely so ngrok can be shut down cleanly later, when the user explicitly says to stop testing — not as part of a relaunch.

1. **Stop the bot narrowly**, verifying the PID actually belongs to the bot before killing it:
   ```bash
   if [ -f /tmp/ci-chat-bot/bot.pid ]; then
     BOT_SESSION=$(cat /tmp/ci-chat-bot/bot.pid)
     if [[ ! "$BOT_SESSION" =~ ^[1-9][0-9]*$ ]]; then
       echo "Invalid bot session ID in bot.pid; not killing. Inspect manually."
       exit 1
     elif ps -eo pid=,pgid=,sid=,args= | awk -v sid="$BOT_SESSION" '
       $2 == sid && $3 == sid &&
         ($0 ~ /(^|[[:space:]])make run([[:space:]]|$)/ || $0 ~ /hack\/run\.sh/ || $0 ~ /ci-chat-bot([[:space:]]|$)/) { found=1 }
       END { exit !found }
     '; then
       kill -TERM -- "-$BOT_SESSION"
       for attempt in {1..30}; do
         if ! ps -eo pgid=,sid=,stat= | awk -v sid="$BOT_SESSION" '$1 == sid && $2 == sid && $3 !~ /^Z/ { found=1 } END { exit !found }'; then
           break
         fi
         sleep 1
       done
       if ps -eo pgid=,sid=,stat= | awk -v sid="$BOT_SESSION" '$1 == sid && $2 == sid && $3 !~ /^Z/ { found=1 } END { exit !found }'; then
         echo "Bot session $BOT_SESSION is still running; do not start another instance. Inspect it manually."
         exit 1
       fi
       rm -f /tmp/ci-chat-bot/bot.pid
     else
       echo "No matching bot process in session $BOT_SESSION; not killing. Inspect manually."
       exit 1
     fi
   fi
   ```
   If `/tmp/ci-chat-bot/bot.pid` is missing or stale (e.g. the bot was started outside this command), do **not** guess with a broad pattern kill. Instead identify it narrowly and confirm with the user first:
   ```bash
   pgrep -fa ci-chat-bot
   pgrep -fa "make run"
   ```
   Show the full command line(s) to the user. Only proceed to kill if there is exactly one unambiguous match and the user confirms it. If there are multiple matches, stop and ask — never kill on an ambiguous/multi-match result.

2. **Rebuild if needed**: Run `make` to pick up code changes.

3. **Start the bot again** per step 5 above (do **not** touch ngrok). The launch wrapper records the new session ID in `/tmp/ci-chat-bot/bot.pid`.

4. **Verify**: tail `/tmp/ci-chat-bot/bot.log` to confirm a clean startup, and confirm ngrok is still forwarding (it was never touched).

## Stopping the Test

When the user says to stop testing entirely, stop **both** processes, each verified narrowly before killing (never a broad pattern match):

```bash
if [ -f /tmp/ci-chat-bot/bot.pid ]; then
  BOT_SESSION=$(cat /tmp/ci-chat-bot/bot.pid)
  if [[ ! "$BOT_SESSION" =~ ^[1-9][0-9]*$ ]]; then
    echo "Invalid bot session ID in bot.pid; not killing. Inspect manually."
  elif ps -eo pid=,pgid=,sid=,args= | awk -v sid="$BOT_SESSION" '
    $2 == sid && $3 == sid &&
      ($0 ~ /(^|[[:space:]])make run([[:space:]]|$)/ || $0 ~ /hack\/run\.sh/ || $0 ~ /ci-chat-bot([[:space:]]|$)/) { found=1 }
    END { exit !found }
  '; then
    kill -TERM -- "-$BOT_SESSION"
    for attempt in {1..30}; do
      if ! ps -eo pgid=,sid=,stat= | awk -v sid="$BOT_SESSION" '$1 == sid && $2 == sid && $3 !~ /^Z/ { found=1 } END { exit !found }'; then
        break
      fi
      sleep 1
    done
    if ps -eo pgid=,sid=,stat= | awk -v sid="$BOT_SESSION" '$1 == sid && $2 == sid && $3 !~ /^Z/ { found=1 } END { exit !found }'; then
      echo "Bot session $BOT_SESSION is still running; inspect it manually."
    else
      rm -f /tmp/ci-chat-bot/bot.pid
    fi
  else
    echo "No matching bot process in session $BOT_SESSION; not killing. Inspect manually."
  fi
fi

if [ -f /tmp/ci-chat-bot/ngrok.pid ]; then
  PID=$(cat /tmp/ci-chat-bot/ngrok.pid)
  if [[ ! "$PID" =~ ^[1-9][0-9]*$ ]]; then
    echo "Invalid PID in ngrok.pid; not killing. Inspect manually."
  elif ps -p "$PID" -o comm=,args= | awk '
    $1 == "ngrok" && $2 ~ /(^|\/)ngrok$/ && $3 == "http" && $4 == "8080" && NF == 4 { found=1 }
    END { exit !found }
  '; then
    if kill -TERM -- "$PID"; then
      for attempt in {1..30}; do
        if ! ps -p "$PID" -o stat= | awk '$1 !~ /^Z/ { found=1 } END { exit !found }'; then
          break
        fi
        sleep 1
      done
      if ps -p "$PID" -o stat= | awk '$1 !~ /^Z/ { found=1 } END { exit !found }'; then
        echo "ngrok process $PID is still running; inspect it manually."
      else
        rm -f /tmp/ci-chat-bot/ngrok.pid
      fi
    else
      echo "Could not signal ngrok process $PID; keeping ngrok.pid. Inspect manually."
    fi
  else
    echo "PID $PID is not the expected ngrok http 8080 process; not killing. Inspect manually."
  fi
fi
```

8. **Provide Troubleshooting Tips** if issues arise:
   - Check logs: `tail -100 /tmp/ci-chat-bot/bot.log` to see recent output
   - If ngrok fails to start, verify it's installed (`ngrok version`)
   - If secrets extraction fails, verify cluster access with `oc --context app.ci whoami`
   - If the bot fails to start, check the error messages in the log file
   - Verify that BOT_TOKEN and BOT_SIGNING_SECRET environment variables are set
   - Check that the required external repositories exist:
     - `../release/ci-operator/jobs/openshift/release/` (job configs)
     - `../release/core-services/prow/02_config/_config.yaml` (prow config)
     - `../release/core-services/ci-chat-bot/workflows-config.yaml` (workflow config)
   - The bot runs with `--disable-rosa` flag and verbose logging (`--v=2`) by default
   - If Slack isn't receiving events, verify the ngrok URL is correctly configured in Slack app settings
   - **GCP Credentials dry-run mode**:
     - Check logs for "DRY-RUN mode" message to confirm it's enabled
     - Verify BigQuery audit logs are still being created
     - Confirm IAM policy remains unchanged in GCP Console
     - If testing credentials command, use: `credentials openshift gcp "test message"`
   - **Process management**: the bot session and ngrok are tracked via `/tmp/ci-chat-bot/bot.pid` and `/tmp/ci-chat-bot/ngrok.pid`. Before stopping the bot, verify its recorded ID matches both the process-group ID and session ID and that a bot launch command is in that session; signal only that process group. Verify ngrok's PID separately. Never use broad-match kill commands (`pkill -f <generic substring>`, `killall`, `pkill node`/`pkill go`/`pkill ngrok`) — they can match unrelated processes, including the coding agent's own process.

## Creating an Environment File Template

If the user wants to create an environment file, offer to create a template for them:

```bash
cat > .env.template << 'EOF'
# Required environment variables
BOT_TOKEN=xoxb-your-bot-token-here
BOT_SIGNING_SECRET=your-signing-secret-here

# Optional: GitHub integration
GITHUB_TOKEN=ghp_your-github-token-here

# Optional: GCP Credentials feature
GCP_ACCESS_DRY_RUN=true
GCP_SERVICE_ACCOUNT_JSON={"type":"service_account","project_id":"your-project",...}
ORG_DATA_BUCKET=your-org-data-bucket

# Add any other environment variables your bot needs
EOF
```

Tell the user to:
1. Copy `.env.template` to `.env`
2. Fill in their actual values
3. Never commit `.env` to git (add it to `.gitignore`)
4. Use `.env` when running the bot with the command from step 5

## Testing the Credentials Command

When running in dry-run mode (`GCP_ACCESS_DRY_RUN=true`), you can safely test the credentials command:

1. In Slack, send: `credentials openshift gcp "Testing dry-run mode"`
2. Check logs for: `grep "DRY-RUN" /tmp/ci-chat-bot/bot.log`
3. You should see messages like:
   - `GCP credentials manager running in DRY-RUN mode`
   - `DRY-RUN: Would grant GCP IAM credentials to user...`
4. Verify BigQuery logs (if configured) are still created
5. Confirm no IAM changes were made in GCP Console

Guide the user through the setup process step by step.
