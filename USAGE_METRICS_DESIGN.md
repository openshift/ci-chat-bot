# CI Chat Bot Usage Metrics Design

## Overview

This document outlines the architecture for comprehensive usage analytics for ci-chat-bot using Google Cloud Storage (GCS) and BigQuery. The system will track command usage, user patterns, resource consumption, and organizational insights without impacting command performance.

## High-Level Architecture

```
Slack User → Auth Middleware → Metrics Collector → GCS Storage → BigQuery Analytics → Insights
    ↓              ↓              ↓               ↓               ↓              ↓
User Action   Event Creation   Buffer/Batch    Partitioned     SQL Queries   Reports
```

## Data Flow

1. **Event Generation**: Enhanced auth middleware captures command attempts
2. **Buffering**: Events collected in memory buffer (100-500 events)
3. **Batch Upload**: Periodic flush to GCS (every 5 minutes or when buffer full)
4. **Storage**: Time-partitioned JSONL files in GCS bucket
5. **Analytics**: BigQuery external tables for real-time SQL queries
6. **Reporting**: Automated reports and dashboards

## Event Data Structure

### Core Event Schema
```json
{
  "event_id": "uuid-123",
  "timestamp": "2025-01-15T14:30:00Z",
  "command": "launch",
  "user": {
    "slack_user_id": "U123456",
    "uid": "ehoxha",
    "full_name": "Enis Hoxha", 
    "teams": ["Platform SRE", "MCE Team"],
    "organizations": ["Engineering"],
    "job_title": "Senior Engineer"
  },
  "action": {
    "platform": "aws",
    "region": "us-east-1", 
    "instance_type": "m5.xlarge",
    "parameters": {
      "cluster_name": "test-cluster-123",
      "openshift_version": "4.14.0",
      "node_count": 3
    }
  },
  "result": {
    "status": "success",
    "duration": "00:05:30",
    "job_id": "prow-job-456",
    "cluster_id": "cluster-789",
    "error_message": null
  }
}
```

### Go Type Definitions
```go
type UsageEvent struct {
    // Event Identity
    EventID     string    `json:"event_id"`
    Timestamp   time.Time `json:"timestamp"`
    Command     string    `json:"command"`
    
    // User Context (enriched from orgdata)
    User UserContext `json:"user"`
    
    // Command Details
    Action ActionDetails `json:"action"`
    
    // Result
    Result ResultDetails `json:"result"`
}

type UserContext struct {
    SlackUserID   string   `json:"slack_user_id"`
    UID           string   `json:"uid"`
    FullName      string   `json:"full_name"`
    Teams         []string `json:"teams"`
    Organizations []string `json:"organizations"`
    JobTitle      string   `json:"job_title,omitempty"`
}

type ActionDetails struct {
    Platform      string                 `json:"platform,omitempty"`
    Region        string                 `json:"region,omitempty"`
    InstanceType  string                 `json:"instance_type,omitempty"`
    ClusterName   string                 `json:"cluster_name,omitempty"`
    Parameters    map[string]interface{} `json:"parameters"`
}

type ResultDetails struct {
    Status       string        `json:"status"`          // "success", "failure", "denied", "timeout"
    Duration     time.Duration `json:"duration"`
    ErrorMessage string        `json:"error_message,omitempty"`
    JobID        string        `json:"job_id,omitempty"`
    ClusterID    string        `json:"cluster_id,omitempty"`
}
```

## GCS Storage Strategy

### Bucket Structure
```
ci-bot-usage-metrics/
├── events/
│   ├── year=2025/
│   │   ├── month=01/
│   │   │   ├── day=15/
│   │   │   │   ├── hour=14/
│   │   │   │   │   └── events-20250115-14-batch001.jsonl
│   │   │   │   └── hour=15/
│   │   │   └── day=16/
│   │   └── month=02/
│   └── year=2026/
├── reports/
│   ├── daily/
│   ├── weekly/ 
│   └── monthly/
└── schemas/
    └── usage-event-v1.json
```

### File Naming Convention
```
events-{YYYYMMDD}-{HH}-{batch_uuid}.jsonl

Examples:
- events-20250115-14-a1b2c3d4.jsonl
- events-20250115-14-e5f6g7h8.jsonl
```

**Benefits:**
- **Time-based partitioning**: Efficient for time-range queries
- **Batch identification**: Unique UUIDs prevent filename collisions
- **Hive-compatible**: Works with BigQuery auto-discovery
- **Human readable**: Easy to understand and debug

## Collection Implementation

### Metrics Collector Interface
```go
type MetricsCollector interface {
    // Record single event
    RecordEvent(ctx context.Context, event UsageEvent) error
    
    // Batch operations for efficiency
    RecordEvents(ctx context.Context, events []UsageEvent) error
    
    // Force flush pending events
    Flush(ctx context.Context) error
    
    // Health check
    Health() error
}
```

### GCS Implementation Architecture
```go
type GCSMetricsCollector struct {
    // GCS Configuration
    client        *storage.Client
    bucket        string
    
    // Buffering Configuration
    batchSize     int           // Default: 100-500 events
    flushInterval time.Duration // Default: 5 minutes
    
    // Internal State
    eventBuffer   []UsageEvent
    bufferMutex   sync.Mutex
    
    // Background Processing
    flushTicker   *time.Ticker
    eventChan     chan UsageEvent  // Non-blocking channel
    done          chan struct{}
    
    // Metrics
    droppedEvents  prometheus.Counter
    uploadedEvents prometheus.Counter
    uploadErrors   prometheus.Counter
}
```

### Event Collection Flow
1. **Command Execution**: Auth middleware captures event
2. **Async Queuing**: Event sent to non-blocking channel
3. **Background Processing**: Goroutine consumes events, buffers them
4. **Batch Upload**: When buffer full or timer expires, upload to GCS
5. **Error Handling**: Failed uploads retried, events never block commands

## BigQuery Integration

### External Table Setup
```sql
CREATE OR REPLACE EXTERNAL TABLE `ci_analytics.usage_events_external`
OPTIONS (
  format = 'NEWLINE_DELIMITED_JSON',
  uris = ['gs://ci-bot-usage-metrics/events/*/*/*/*/*.jsonl'],
  max_staleness = INTERVAL 5 MINUTE  -- Cache for 5 minutes
)
AS SELECT
  event_id,
  timestamp,
  command,
  user.slack_user_id,
  user.uid,
  user.full_name,
  user.teams,
  user.organizations,
  action.platform,
  action.region,
  action.instance_type,
  action.parameters,
  result.status,
  result.duration,
  result.job_id,
  result.cluster_id
FROM external_data;
```

### Partitioned View for Performance
```sql
CREATE OR REPLACE VIEW `ci_analytics.usage_events_partitioned` AS
SELECT *
FROM `ci_analytics.usage_events_external`
WHERE timestamp >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 2 YEAR);
```

## Analytics Queries

### Team Usage Analysis
```sql
-- Most active teams by command type
SELECT 
  team,
  command,
  COUNT(*) as usage_count,
  COUNT(DISTINCT user.uid) as unique_users,
  COUNTIF(result.status = 'success') / COUNT(*) as success_rate,
  AVG(EXTRACT(EPOCH FROM result.duration)) as avg_duration_seconds
FROM `ci_analytics.usage_events_external`,
UNNEST(user.teams) as team
WHERE timestamp >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 30 DAY)
GROUP BY team, command
ORDER BY usage_count DESC;
```

### Platform Adoption Trends
```sql
-- AWS vs GCP adoption over time
SELECT 
  DATE_TRUNC(timestamp, WEEK) as week,
  action.platform,
  COUNT(*) as launches,
  COUNT(DISTINCT user.uid) as unique_users,
  COUNT(DISTINCT result.cluster_id) as successful_clusters
FROM `ci_analytics.usage_events_external`
WHERE command = 'launch'
  AND result.status = 'success'
  AND timestamp >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 90 DAY)
GROUP BY week, action.platform
ORDER BY week DESC, platform;
```

### Resource Optimization
```sql
-- Most expensive instance types by team
SELECT 
  team,
  action.instance_type,
  action.region,
  COUNT(*) as cluster_count,
  AVG(EXTRACT(EPOCH FROM result.duration)) as avg_launch_time_seconds
FROM `ci_analytics.usage_events_external`,
UNNEST(user.teams) as team  
WHERE command = 'launch'
  AND result.status = 'success'
  AND timestamp >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 30 DAY)
GROUP BY team, action.instance_type, action.region
ORDER BY cluster_count DESC;
```

### Authorization Analysis
```sql
-- Commands being denied - potential policy issues
SELECT 
  command,
  COUNT(*) as denial_count,
  COUNT(DISTINCT user.uid) as affected_users,
  ARRAY_AGG(DISTINCT CONCAT(user.full_name, ' (', user.uid, ')') LIMIT 10) as example_users
FROM `ci_analytics.usage_events_external`
WHERE result.status = 'denied'
  AND timestamp >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 7 DAY)
GROUP BY command
ORDER BY denial_count DESC;
```

## Implementation Architecture

### Enhanced Authorization Middleware
```go
func (a *AuthorizationService) CheckAuthorizationWithMetrics(
    ctx context.Context,
    slackUserID, command string,
    parameters map[string]interface{},
    collector MetricsCollector,
) (bool, string, string) {  // Returns: authorized, message, eventID
    
    eventID := uuid.New().String()
    start := time.Now()
    
    // Get user context for metrics
    userContext := a.buildUserContext(slackUserID)
    
    // Check authorization
    allowed, message := a.CheckAuthorization(slackUserID, command)
    
    // Record authorization event
    event := UsageEvent{
        EventID:   eventID,
        Timestamp: start,
        Command:   command,
        User:      userContext,
        Action: ActionDetails{
            Parameters: filterSensitiveParams(parameters),
        },
        Result: ResultDetails{
            Status:   map[bool]string{true: "authorized", false: "denied"}[allowed],
            Duration: time.Since(start),
            ErrorMessage: map[bool]string{true: "", false: message}[allowed],
        },
    }
    
    // Async metrics collection (never blocks commands)
    go func() {
        if err := collector.RecordEvent(context.Background(), event); err != nil {
            // Log error but don't fail the command
            log.Printf("Failed to record usage event: %v", err)
        }
    }()
    
    return allowed, message, eventID
}

func (a *AuthorizationService) buildUserContext(slackUserID string) UserContext {
    employee := a.orgDataService.GetEmployeeBySlackID(slackUserID)
    teams := a.orgDataService.GetTeamsForSlackID(slackUserID)
    orgs := a.orgDataService.GetUserOrganizations(slackUserID)
    
    var orgNames []string
    for _, org := range orgs {
        orgNames = append(orgNames, org.Name)
    }
    
    return UserContext{
        SlackUserID:   slackUserID,
        UID:           employee.UID,
        FullName:      employee.FullName,
        Teams:         teams,
        Organizations: orgNames,
        JobTitle:      employee.JobTitle,
    }
}
```

### GCS Metrics Collector
```go
type GCSMetricsCollector struct {
    // GCS Configuration
    client        *storage.Client
    bucket        string
    objectPrefix  string  // "events/"
    
    // Buffering Configuration
    batchSize     int           // Default: 500 events
    flushInterval time.Duration // Default: 5 minutes
    maxRetries    int           // Default: 3
    
    // Internal State
    eventBuffer   []UsageEvent
    bufferMutex   sync.Mutex
    
    // Background Processing
    eventChan     chan UsageEvent
    flushChan     chan struct{}
    done          chan struct{}
    wg            sync.WaitGroup
    
    // Monitoring
    droppedEvents  prometheus.Counter
    uploadedEvents prometheus.Counter
    uploadErrors   prometheus.Counter
    bufferSize     prometheus.Gauge
}

func NewGCSMetricsCollector(config GCSMetricsConfig) *GCSMetricsCollector {
    return &GCSMetricsCollector{
        client:        config.Client,
        bucket:        config.Bucket,
        objectPrefix:  config.ObjectPrefix,
        batchSize:     config.BatchSize,
        flushInterval: config.FlushInterval,
        eventChan:     make(chan UsageEvent, 1000), // Non-blocking buffer
        flushChan:     make(chan struct{}, 1),
        done:          make(chan struct{}),
    }
}

func (g *GCSMetricsCollector) Start(ctx context.Context) {
    g.wg.Add(1)
    go g.backgroundProcessor(ctx)
}

func (g *GCSMetricsCollector) backgroundProcessor(ctx context.Context) {
    defer g.wg.Done()
    
    ticker := time.NewTicker(g.flushInterval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            g.flushBuffer(ctx) // Final flush
            return
            
        case event := <-g.eventChan:
            g.addToBuffer(event)
            
        case <-ticker.C:
            g.flushBuffer(ctx)
            
        case <-g.flushChan:
            g.flushBuffer(ctx)
        }
    }
}

func (g *GCSMetricsCollector) RecordEvent(ctx context.Context, event UsageEvent) error {
    select {
    case g.eventChan <- event:
        return nil
    default:
        // Channel full, increment dropped counter but don't block
        g.droppedEvents.Inc()
        return ErrBufferFull
    }
}

func (g *GCSMetricsCollector) addToBuffer(event UsageEvent) {
    g.bufferMutex.Lock()
    defer g.bufferMutex.Unlock()
    
    g.eventBuffer = append(g.eventBuffer, event)
    g.bufferSize.Set(float64(len(g.eventBuffer)))
    
    // Force flush if buffer is full
    if len(g.eventBuffer) >= g.batchSize {
        select {
        case g.flushChan <- struct{}{}:
        default:
            // Flush already pending
        }
    }
}

func (g *GCSMetricsCollector) flushBuffer(ctx context.Context) error {
    g.bufferMutex.Lock()
    eventsToFlush := make([]UsageEvent, len(g.eventBuffer))
    copy(eventsToFlush, g.eventBuffer)
    g.eventBuffer = g.eventBuffer[:0] // Clear buffer
    g.bufferMutex.Unlock()
    
    if len(eventsToFlush) == 0 {
        return nil
    }
    
    return g.uploadEvents(ctx, eventsToFlush)
}

func (g *GCSMetricsCollector) uploadEvents(ctx context.Context, events []UsageEvent) error {
    // Generate time-partitioned object name
    timestamp := events[0].Timestamp
    objectName := g.generateObjectPath(timestamp)
    
    // Convert events to JSONL format
    var lines []string
    for _, event := range events {
        if jsonData, err := json.Marshal(event); err == nil {
            lines = append(lines, string(jsonData))
        }
    }
    
    // Upload to GCS with retry logic
    return g.uploadWithRetry(ctx, objectName, strings.Join(lines, "\n"))
}

func (g *GCSMetricsCollector) generateObjectPath(timestamp time.Time) string {
    return fmt.Sprintf(
        "%sevents/year=%d/month=%02d/day=%02d/hour=%02d/events-%s-%s.jsonl",
        g.objectPrefix,
        timestamp.Year(),
        timestamp.Month(),
        timestamp.Day(),
        timestamp.Hour(),
        timestamp.Format("20060102-15"),
        uuid.New().String()[:8],
    )
}
```

## BigQuery Integration

### External Table Creation
```sql
-- Create dataset for analytics
CREATE SCHEMA IF NOT EXISTS `ci_analytics`
OPTIONS (
  description = "CI Chat Bot usage analytics",
  location = "US"
);

-- External table pointing to GCS
CREATE OR REPLACE EXTERNAL TABLE `ci_analytics.usage_events_raw`
OPTIONS (
  format = 'NEWLINE_DELIMITED_JSON',
  uris = ['gs://ci-bot-usage-metrics/events/*/*/*/*/*.jsonl'],
  max_staleness = INTERVAL 5 MINUTE
);

-- Create partitioned view for better performance
CREATE OR REPLACE VIEW `ci_analytics.usage_events` AS
SELECT 
  event_id,
  timestamp,
  command,
  user.slack_user_id,
  user.uid,
  user.full_name,
  user.teams,
  user.organizations,
  user.job_title,
  action.platform,
  action.region,
  action.instance_type,
  action.cluster_name,
  action.parameters,
  result.status,
  EXTRACT(EPOCH FROM result.duration) as duration_seconds,
  result.job_id,
  result.cluster_id,
  result.error_message,
  
  -- Derived fields for easier analysis
  DATE(timestamp) as event_date,
  EXTRACT(HOUR FROM timestamp) as event_hour,
  EXTRACT(DAYOFWEEK FROM timestamp) as day_of_week
FROM `ci_analytics.usage_events_raw`
WHERE timestamp >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 2 YEAR);
```

## Analytics Examples

### 1. Team Usage Dashboard
```sql
-- Current month team activity
SELECT 
  team,
  command,
  COUNT(*) as total_commands,
  COUNT(DISTINCT user.uid) as active_users,
  COUNTIF(result.status = 'success') as successful_commands,
  ROUND(COUNTIF(result.status = 'success') / COUNT(*) * 100, 1) as success_rate_pct,
  ROUND(AVG(duration_seconds), 1) as avg_duration_sec
FROM `ci_analytics.usage_events`,
UNNEST(user.teams) as team
WHERE event_date >= DATE_TRUNC(CURRENT_DATE(), MONTH)
GROUP BY team, command
ORDER BY total_commands DESC;
```

### 2. Platform Cost Analysis
```sql
-- Resource usage by platform and instance type
WITH resource_usage AS (
  SELECT 
    action.platform,
    action.instance_type,
    action.region,
    team,
    COUNT(*) as launches,
    -- Estimate costs (could be joined with actual billing data)
    CASE 
      WHEN action.instance_type = 'm5.large' THEN COUNT(*) * 0.096 * 24
      WHEN action.instance_type = 'm5.xlarge' THEN COUNT(*) * 0.192 * 24
      WHEN action.instance_type = 'm5.2xlarge' THEN COUNT(*) * 0.384 * 24
      ELSE 0
    END as estimated_daily_cost_usd
  FROM `ci_analytics.usage_events`,
  UNNEST(user.teams) as team
  WHERE command = 'launch'
    AND result.status = 'success'
    AND event_date >= CURRENT_DATE() - 30
  GROUP BY action.platform, action.instance_type, action.region, team
)
SELECT 
  team,
  platform,
  SUM(launches) as total_launches,
  SUM(estimated_daily_cost_usd) as estimated_monthly_cost_usd,
  SUM(estimated_daily_cost_usd) / SUM(launches) as cost_per_launch
FROM resource_usage
GROUP BY team, platform
ORDER BY estimated_monthly_cost_usd DESC;
```

### 3. Authorization Effectiveness
```sql
-- Analyze authorization denials
SELECT 
  command,
  COUNT(*) as total_attempts,
  COUNTIF(result.status = 'denied') as denied_attempts,
  ROUND(COUNTIF(result.status = 'denied') / COUNT(*) * 100, 1) as denial_rate_pct,
  COUNT(DISTINCT user.uid) as unique_users_affected,
  ARRAY_AGG(DISTINCT user.full_name IGNORE NULLS LIMIT 5) as example_denied_users
FROM `ci_analytics.usage_events`
WHERE timestamp >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 7 DAY)
GROUP BY command
HAVING denial_rate_pct > 5  -- Focus on commands with >5% denial rate
ORDER BY denied_attempts DESC;
```

### 4. Performance Monitoring
```sql
-- Command execution time trends
SELECT 
  command,
  DATE_TRUNC(timestamp, DAY) as date,
  COUNT(*) as executions,
  ROUND(PERCENTILE_CONT(duration_seconds, 0.5) OVER(), 1) as p50_duration,
  ROUND(PERCENTILE_CONT(duration_seconds, 0.95) OVER(), 1) as p95_duration,
  ROUND(PERCENTILE_CONT(duration_seconds, 0.99) OVER(), 1) as p99_duration
FROM `ci_analytics.usage_events`
WHERE result.status = 'success'
  AND timestamp >= CURRENT_DATE() - 30
GROUP BY command, date
ORDER BY date DESC, executions DESC;
```

## Analytics Tool Implementation

### CLI Analytics Tool
```go
// cmd/analytics/main.go
func main() {
    var (
        projectID  = flag.String("project", "openshift-crt", "BigQuery project ID")
        reportType = flag.String("report", "", "Report type: team-usage, platform-trends, cost-analysis")
        team       = flag.String("team", "", "Team name for team-specific reports")
        startDate  = flag.String("start", "", "Start date (YYYY-MM-DD)")
        endDate    = flag.String("end", "", "End date (YYYY-MM-DD)")
        output     = flag.String("output", "console", "Output format: console, json, csv, html")
    )
    
    client, err := bigquery.NewClient(ctx, *projectID)
    if err != nil {
        log.Fatal(err)
    }
    
    analyzer := NewUsageAnalyzer(client)
    
    switch *reportType {
    case "team-usage":
        report, err := analyzer.GenerateTeamReport(*team, parseTimeRange(*startDate, *endDate))
    case "platform-trends":
        report, err := analyzer.GeneratePlatformTrends(parseTimeRange(*startDate, *endDate))
    case "cost-analysis":
        report, err := analyzer.GenerateCostAnalysis(parseTimeRange(*startDate, *endDate))
    default:
        log.Fatal("Unknown report type")
    }
    
    outputReport(report, *output)
}
```

### Automated Report Generation
```go
// pkg/analytics/scheduler.go
type ReportScheduler struct {
    client      *bigquery.Client
    slackClient *slack.Client
    schedules   []ReportSchedule
}

type ReportSchedule struct {
    Name        string
    Query       string
    Schedule    string        // Cron expression
    SlackChannel string       // Where to send results
    Template    string        // Report template
}

func (r *ReportScheduler) ScheduleDailyDigest() {
    // Every Monday at 9 AM, send weekly team usage summary
    schedule := ReportSchedule{
        Name:     "weekly-team-digest",
        Schedule: "0 9 * * 1",  // Cron: Monday 9 AM
        Query:    weeklyTeamUsageQuery,
        SlackChannel: "#platform-metrics",
        Template: "weekly-digest.tmpl",
    }
    
    r.schedules = append(r.schedules, schedule)
}
```

## Operational Considerations

### Performance Impact
- **Memory**: ~100KB buffer (500 events × 200 bytes)
- **CPU**: Minimal (async JSON serialization)
- **Network**: Batched uploads (1 request per 500 events)
- **Latency**: Zero impact on command execution (async collection)

### Reliability & Error Handling
```go
type CircuitBreaker struct {
    failureThreshold int
    resetTimeout     time.Duration
    state           State // Open, Closed, HalfOpen
}

func (g *GCSMetricsCollector) uploadWithCircuitBreaker(ctx context.Context, data string) error {
    if g.circuitBreaker.State() == Open {
        return ErrCircuitBreakerOpen
    }
    
    err := g.doUpload(ctx, data)
    if err != nil {
        g.circuitBreaker.RecordFailure()
        return err
    }
    
    g.circuitBreaker.RecordSuccess()
    return nil
}
```

### Cost Estimation

#### GCS Storage Costs
```
Assumptions:
- 2000 commands/day average
- 800 bytes per event (rich JSON)
- 365 days = ~580MB/year
- Standard storage: $0.020/GB/month

Annual GCS cost: ~$1.50
```

#### BigQuery Query Costs
```
Query pricing: $5/TB scanned
With 580MB/year data:
- Full table scan: $0.003
- Typical queries (1-7 days): $0.0001
- Monthly analytics budget: ~$5

Total annual cost: < $25 (extremely cost-effective!)
```

### Privacy & Security

#### Data Filtering
```go
func filterSensitiveParams(params map[string]interface{}) map[string]interface{} {
    filtered := make(map[string]interface{})
    
    sensitiveKeys := map[string]bool{
        "password":    true,
        "token":       true,
        "secret":      true,
        "private_key": true,
    }
    
    for key, value := range params {
        if !sensitiveKeys[strings.ToLower(key)] {
            filtered[key] = value
        } else {
            filtered[key] = "[REDACTED]"
        }
    }
    
    return filtered
}
```

#### Access Control
- **BigQuery**: Dataset-level IAM (analytics team only)
- **GCS Bucket**: Object viewer role for report generation
- **Retention**: Automatic deletion after 2 years
- **Audit**: Track who runs analytics queries

## Implementation Phases

### Phase 1: Foundation (Week 1)
- Create `GCSMetricsCollector`
- Enhance auth middleware with event collection
- Basic GCS bucket setup with partitioning

### Phase 2: BigQuery Setup (Week 2)  
- Create external tables
- Basic queries for team and platform usage
- Simple CLI analytics tool

### Phase 3: Rich Analytics (Week 3)
- Complex cross-organizational queries
- Performance and cost analysis
- Automated report generation

### Phase 4: Advanced Features (Month 2)
- Real-time dashboards (Data Studio)
- Predictive analytics (ML)
- Cost attribution with billing data
- Slack integration for automated reports

## Future Extensions

### Advanced Data Sources
- **Cost Attribution**: Join with GCP billing exports
- **Performance Data**: Correlate with cluster performance metrics
- **User Satisfaction**: Track command success/failure patterns

### Machine Learning Opportunities
```sql
-- BigQuery ML for usage prediction
CREATE MODEL `ci_analytics.usage_prediction`
OPTIONS (
  model_type = 'linear_reg',
  input_label_cols = ['daily_usage']
) AS
SELECT 
  team,
  EXTRACT(DAYOFWEEK FROM event_date) as day_of_week,
  EXTRACT(WEEK FROM event_date) as week_of_year,
  COUNT(*) as daily_usage
FROM `ci_analytics.usage_events`
WHERE event_date >= DATE_SUB(CURRENT_DATE(), INTERVAL 180 DAY)
GROUP BY team, event_date;
```

### Integration Possibilities
- **Slack reporting**: Daily/weekly digest in team channels
- **Grafana dashboards**: Real-time operational metrics
- **Cost optimization**: Automated recommendations
- **Capacity planning**: Predict resource needs

## Configuration

### Environment Variables
```bash
# Metrics collection
export METRICS_ENABLED=true
export METRICS_GCS_BUCKET="ci-bot-usage-metrics"
export METRICS_BATCH_SIZE=500
export METRICS_FLUSH_INTERVAL=5m

# BigQuery analytics
export BIGQUERY_PROJECT_ID="openshift-crt"
export BIGQUERY_DATASET="ci_analytics"
```

This design gives you **enterprise-grade usage intelligence** with:
- 🔍 **Rich SQL analytics** on years of historical data
- 📊 **Real-time insights** with minimal latency  
- 💰 **Cost-effective scale** that grows with your usage
- 🛡️ **Production reliability** that never impacts user commands
- 🚀 **Future-ready** for ML and advanced analytics

Ready to start with Phase 1 implementation when you're ready!


