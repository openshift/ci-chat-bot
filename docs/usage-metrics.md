# Cluster Bot usage metrics

Cluster Bot records recognized action commands received in direct messages.
`help`, bot messages, edited/file-share events, channel messages, and
unrecognized text are not counted. Command labels are normalized to a bounded
set; request arguments, justifications, email addresses, and display names are
not labels.

The usage counters use these membership values:

- `member`: the employee was found and is a member of `Hybrid Platforms`.
- `non_member`: the employee was found and is not a member of `Hybrid Platforms`.
- `unknown`: the employee or organizational data could not be resolved.

`slack_user_id` is the Slack ID from the event. It is intentionally retained
to support distinct-user queries. Metrics access controls and retention must
be appropriate for this internal identifier. Before rollout, estimate
cardinality as active users × commands used × membership states and confirm
that the Prometheus budget can accommodate it.

## PromQL

Replace `30d` in the queries below with `7d` or `90d` for the other requested
observation windows.

Distinct users by membership:

```promql
count by (hybrid_platforms_membership) (
  count by (slack_user_id, hybrid_platforms_membership) (
    increase(ci_chat_bot_user_command_activity_total[30d]) > 0
  )
)
```

Distinct confirmed non-member Cluster Bot users:

```promql
count(
  count by (slack_user_id) (
    increase(ci_chat_bot_user_command_activity_total{
      hybrid_platforms_membership="non_member"
    }[30d]) > 0
  )
)
```

Slack IDs of non-members using Cluster Bot:

```promql
sum by (slack_user_id) (
  increase(ci_chat_bot_user_command_activity_total{
    hybrid_platforms_membership="non_member"
  }[30d])
) > 0
```

Distinct non-members attempting `request gcp-access`:

```promql
count(
  count by (slack_user_id) (
    increase(ci_chat_bot_user_command_activity_total{
      command="request-gcp-access",
      hybrid_platforms_membership="non_member"
    }[30d]) > 0
  )
)
```

Slack IDs of non-members attempting GCP access, with attempt counts:

```promql
sum by (slack_user_id) (
  increase(ci_chat_bot_user_command_activity_total{
    command="request-gcp-access",
    hybrid_platforms_membership="non_member"
  }[30d])
) > 0
```

Total GCP access attempts by membership:

```promql
sum by (hybrid_platforms_membership) (
  increase(ci_chat_bot_user_command_activity_total{
    command="request-gcp-access"
  }[30d])
)
```

Confirmed denial rate among known GCP request outcomes (unknown memberships
are omitted, and grant errors are not counted as confirmed denials or grants):

```promql
sum(increase(ci_chat_bot_gcp_access_requests_total{outcome="denied"}[30d]))
/
sum(increase(ci_chat_bot_gcp_access_requests_total{
  outcome=~"granted|denied"
}[30d]))
```

Unknown membership percentage for recognized command activity:

```promql
sum(increase(ci_chat_bot_command_executions_total{
  hybrid_platforms_membership="unknown"
}[30d]))
/
sum(increase(ci_chat_bot_command_executions_total[30d]))
```

Treat an unknown ratio above 10% over the observation window as elevated:
investigate the org-data source and avoid drawing non-member conclusions until
the ratio returns below that threshold or the affected interval is excluded.

## Rollout decision inputs

Collect a representative 30–90 day observation period and review:

- unique confirmed non-member requesters;
- the percentage of non-member bot users who attempt GCP access;
- repeat attempts per requester;
- the unknown membership ratio and any outage intervals; and
- project capacity and security implications, in addition to request volume.

This document provides manual queries only. Dashboard and alert creation are
out of scope.
