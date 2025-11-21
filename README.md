# ci-chat-bot (aka cluster-bot)
The **@cluster-bot** is a Slack App that allows users the ability to launch and test OpenShift Clusters from any existing custom built releases.

The App is currently running in the Red Hat Internal slack workspace. To begin using the cluster-bot: Select the `+` in the `Apps` section, to `Browse Apps`, search for `cluster-bot`, and then select its tile.
You'll be placed in a new channel with the App, and you'll be ready to begin launching clusters!

To see the available commands, type `help`.

## Features

### 🔐 **Advanced Authorization System**
- **Organizational data-based access control** using pre-computed indexes for fast lookups
- **Multiple authorization levels**: User UID, team membership, organization-based permissions  
- **Hot reload**: Automatic updates when organizational data or authorization config changes
- **Complete hierarchy support**: Teams → Organizations → Pillars → Team Groups

### ☁️ **Flexible Data Sources**
- **Local files**: Development and testing with JSON files
- **Google Cloud Storage**: Production deployments with secure, cross-cluster access
- **Hot reload**: Both file watching and GCS polling for live updates
- **Pluggable architecture**: Easy to extend with new data sources

### 🚀 **Production Ready**  
- **Fast performance**: O(1) organizational lookups with pre-computed indexes
- **Thread-safe**: Concurrent access with read-write mutex protection
- **Build flexibility**: Optional GCS support with build tags (`-tags gcs`)
- **Secure authentication**: Application Default Credentials for GCS

## Quick Start

### Option 1: Local Development
```bash
# Set your organizational data file
export ORGDATA_PATHS="/path/to/comprehensive_index.json"

# Start the bot
./hack/run.sh
```

### Option 2: Google Cloud Storage
```bash
# Build with GCS support
make BUILD_FLAGS="-tags gcs" build

# Quick start with GCS
./hack/run-with-gcs.sh

# Or configure manually
export USE_GCS_ORGDATA=true
export GCS_BUCKET="your-bucket"
export GCS_OBJECT_PATH="orgdata/comprehensive_index.json"
./hack/run.sh
```

### Check Your Permissions
```
@cluster-bot whoami
```

## Documentation

- 📖 **[AUTHORIZATION.md](AUTHORIZATION.md)** - Complete authorization system setup and configuration
- 🛠️ **[hack/DEVELOPMENT.md](hack/DEVELOPMENT.md)** - Detailed development setup guide
- ❓ **[docs/FAQ.md](docs/FAQ.md)** - Frequently asked questions

## Build Options

```bash
# Standard build (file-based data sources only)
make build

# Build with GCS support
make BUILD_FLAGS="-tags gcs" build

# See all available targets
make help-ci-chat-bot
```

## Getting Help

For any questions, concerns, comments, etc, please reach out in the `#forum-ocp-crt` channel.

## Links
* [OpenShift Releases](https://amd64.ocp.releases.ci.openshift.org/)
* [Authorization System Documentation](AUTHORIZATION.md) - Complete setup guide
* [Development Guide](hack/DEVELOPMENT.md) - Local development setup  
* [Frequently Asked Questions](docs/FAQ.md)
* [Makefile Help](Makefile) - Run `make help-ci-chat-bot` for build options
