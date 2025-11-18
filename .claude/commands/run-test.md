---
description: Run a test instance of the ci-chat-bot
---

You are helping the user run a test instance of the ci-chat-bot. Follow these steps:

## Security Warning

**IMPORTANT - Security Notice**: This command will ask you to provide Slack credentials during setup.

- Do NOT share the chat transcript or logs containing these credentials with others
- Credentials will be visible in process listings (`ps aux`) while the bot is running
- The ngrok tunnel exposes your local bot instance to the internet - only use test/development Slack apps
- Logs at `/tmp/ci-chat-bot.log` may contain sensitive information
- For production deployments, use proper secret management (Kubernetes secrets, vault, etc.) instead of environment variables

1. **Check Environment Variables**: First verify that the required environment variables are set by checking BOT_TOKEN and BOT_SIGNING_SECRET.

   If they are NOT set, ask the user to provide:
   - `BOT_TOKEN`: Slack Bot Token (required) - starts with `xoxb-`
   - `BOT_SIGNING_SECRET`: Slack App Signing Secret (required)
   - `GITHUB_TOKEN`: GitHub token (optional but recommended)

   Store these values to use in step 5. Tell the user where to find these values:
   - Go to https://api.slack.com/apps
   - Select their app
   - **BOT_TOKEN**: OAuth & Permissions → Bot User OAuth Token
   - **BOT_SIGNING_SECRET**: Basic Information → App Credentials → Signing Secret

2. **Verify Cluster Access**: Confirm the user has `oc` CLI access to the `app.ci` cluster context:
   - Run `oc --context app.ci whoami` to verify access
   - If this fails, the user needs to authenticate to the OpenShift CI cluster first

3. **Setup ngrok Tunnel**: Start ngrok to expose the bot to Slack:
   - Run `ngrok http 8080` in the background
   - Extract and display the public HTTPS URL that ngrok provides
   - The URL will look like: `https://xxxx-xx-xx-xx-xx.ngrok-free.app`
   - Inform the user they need to configure this URL in their Slack app settings:
     - Go to the Slack app configuration page (https://api.slack.com/apps)
     - Navigate to "Interactivity & Shortcuts"
       - Set Request URL to: `https://xxxx-xx-xx-xx-xx.ngrok-free.app/slack/interactive-endpoint`
     - Navigate to "Event Subscriptions"
       - Set Request URL to: `https://xxxx-xx-xx-xx-xx.ngrok-free.app/slack/events-endpoint`

4. **Build the Project**: Run `make` to build the ci-chat-bot binary.

5. **Run the Full Setup**: Execute the complete setup with log redirection using the BOT_TOKEN and BOT_SIGNING_SECRET values obtained in step 1:
   ```bash
   BOT_TOKEN=<token-from-step-1> BOT_SIGNING_SECRET=<secret-from-step-1> make run > /tmp/ci-chat-bot.log 2>&1 &
   ```

   Use the actual values provided by the user in step 1. This will:
   - Extract kubeconfig files from the `ci-chat-bot-kubeconfigs` secret
   - Get Boskos credentials from the `boskos-credentials` secret
   - Extract ROSA configuration (subnet IDs, OIDC config ID, billing account ID)
   - Extract MCE kubeconfig and token
   - Build the binary if needed
   - Start the bot with all required configuration
   - Redirect all output to `/tmp/ci-chat-bot.log` for easy monitoring

6. **Verify the Bot is Running**:
   - Check that the bot starts without errors
   - By default it listens on port 8080
   - Verify ngrok is still running and forwarding requests
   - Monitor logs with: `tail -f /tmp/ci-chat-bot.log`
   - Test basic Slack connectivity by sending a message to the bot in Slack

7. **Inform User About Log Monitoring**: After starting the bot, inform the user:
   - Logs are saved to `/tmp/ci-chat-bot.log`
   - They can monitor logs in real-time with: `tail -f /tmp/ci-chat-bot.log`
   - To filter for errors: `tail -f /tmp/ci-chat-bot.log | grep -i error`
   - To filter for warnings: `tail -f /tmp/ci-chat-bot.log | grep -i warning`

8. **Provide Troubleshooting Tips** if issues arise:
   - Check logs: `tail -100 /tmp/ci-chat-bot.log` to see recent output
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

Guide the user through the setup process step by step.
