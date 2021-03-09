#!/bin/bash

# This script ensures that every PR has all the required updates:
#     > gofmt .
#     > go mod vendor
#     > go mod tidy

set -o errexit
set -o nounset
set -o pipefail

base_dir="${1:-}"

if [[ ! -d "${base_dir}" ]]; then
  echo "Expected a single argument: a path to the root directory of the ci-chat-bot"
  exit 1
fi

cat << EOF
INFO: This check enforces that all ci-chat-bot PRs conform to a consistent coding
INFO: standard and have the necessary vendoring changes included as part of the
INFO: pending PR.

INFO: If you receive an error, you will need to run the following commands and
INFO: include any changes in your PR:
INFO:      # gofmt <path_to_go_Code>
INFO:      # go mod vendor
INFO:      # go mod tidy

EOF

echo "Checking go formatting..."
(cd ${base_dir}; gofmt -w $(find . -type f -a -name '*.go' | grep -v /vendor/) && git add --intent-to-add . && git diff --quiet --exit-code .)

echo "Checking go vendoring..."
(cd ${base_dir}; go mod vendor && git add --intent-to-add . && git diff --quiet --exit-code .)

echo "Checking go mod tidy..."
(cd ${base_dir}; go mod tidy && git add --intent-to-add . && git diff --quiet --exit-code .)

exit 0
