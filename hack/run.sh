#!/usr/bin/env bash

set -euo pipefail

if [[ -z $BOT_TOKEN ]]; then echo "BOT_TOKEN var must be set"; exit 1; fi
if [[ -z $BOT_SIGNING_SECRET ]]; then echo "BOT_SIGNING_SECRET var must be set"; exit 1; fi

work_dir=$(readlink -f $(dirname $0)/..)

tmp_dir=$(mktemp -d)
tmp_kube=$tmp_dir/kubeconfigs
mkdir $tmp_kube
tmp_boskos=$tmp_dir/boskos
trap 'rm -rf $tmp_dir' EXIT

cd $tmp_dir
oc --context app.ci -n ci get secrets ci-chat-bot-kubeconfigs -o json > ci-chat-bot-kubeconfigs.json
for key in `cat ci-chat-bot-kubeconfigs.json | jq -r '.data | keys[]'`
do
  cat ci-chat-bot-kubeconfigs.json | jq -r ".data[\"$key\"]" | base64 -d > ${tmp_kube}/${key}
done

oc --context app.ci -n ci get secrets boskos-credentials -ogo-template={{.data.credentials}} | base64 -d > $tmp_boskos

cd $work_dir
make
./ci-chat-bot \
  --force-pr-owner=system:serviceaccount:ci:ci-chat-bot \
  --job-config ${work_dir}/../release/ci-operator/jobs/openshift/release/ \
  --prow-config ${work_dir}/../release/core-services/prow/02_config/_config.yaml \
  --workflow-config-path ${work_dir}/../release/core-services/ci-chat-bot/workflows-config.yaml \
  --lease-server-credentials-file $tmp_boskos \
  --override-launch-label "ci-chat-bot-alpha.openshift.io/launch" \
  --kubeconfig-dir $tmp_kube \
  --kubeconfig-suffix=.config \
  --v=2
