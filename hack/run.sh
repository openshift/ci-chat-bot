#!/usr/bin/env bash

set -euo pipefail

if [[ -z $BOT_TOKEN ]]; then echo "BOT_TOKEN var must be set"; exit 1; fi
if [[ -z $BOT_APP_TOKEN ]]; then echo "BOT_APP_TOKEN var must be set"; exit 1; fi

tmp_dir=$(mktemp -d)
tmp_kk=$tmp_dir/build.kubeconfig
trap 'rm -rf $tmp_dir' EXIT

kubectl config view >$tmp_kk
kubectl --kubeconfig=$tmp_kk config use-context app.ci


kubectl config use-context app.ci
cd $(dirname $0)/..
make
./ci-chat-bot \
  --force-pr-owner=system:serviceaccount:ci:ci-chat-bot \
  --job-config ../release/ci-operator/jobs/openshift/release/ \
  --prow-config ../release/core-services/prow/02_config/_config.yaml \
  --build-cluster-kubeconfigs-location=$tmp_dir \
  --workflow-config-path ../release/core-services/ci-chat-bot/workflows-config.yaml
  --v=2
