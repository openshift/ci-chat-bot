# ci-chat-bot (aka cluster-bot)
The **@cluster-bot** is a Slack App that allows users the ability to launch and test OpenShift Clusters from any existing custom built releases.

The App is currently running in the Red Hat Internal slack workspace. To begin using the cluster-bot: Select the `+` in the `Apps` section, to `Browse Apps`, search for `cluster-bot`, and then select its tile.
You'll be placed in a new channel with the App, and you'll be ready to begin launching clusters!

To see the available commands, type `help`.

## Authorization System

The cluster-bot includes an organizational data-based authorization system that controls access to commands based on team membership, organization affiliation, or individual user permissions. 

- Use `@cluster-bot whoami` to see your permissions and available commands
- Administrators can configure access rules in `authorization.yaml`
- See [AUTHORIZATION.md](AUTHORIZATION.md) for detailed setup and configuration instructions

For any questions, concerns, comments, etc, please reach out in the `#forum-ocp-crt` channel.

## Links
* [OpenShift Releases](https://amd64.ocp.releases.ci.openshift.org/)
* [Frequently Asked Questions](docs/FAQ.md)
* [Authorization System Documentation](AUTHORIZATION.md)
