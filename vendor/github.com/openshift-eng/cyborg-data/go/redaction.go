package orgdatacore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// RedactingDataSource is a DataSource decorator that redacts PII fields
// from loaded data. When pii_mode is PIIModeRedacted, it intercepts the
// JSON stream from the inner source and strips PII fields before the
// data is parsed by Service.
//
// PII fields redacted:
//   - full_name → "[REDACTED]"
//   - email → "[REDACTED]"
//   - slack_uid → ""
//   - github_id → ""
//
// PII indexes cleared:
//   - slack_id_mappings.slack_uid_to_uid → {}
//   - github_id_mappings.github_id_to_uid → {}
type RedactingDataSource struct {
	source  DataSource
	piiMode PIIMode
}

// NewRedactingDataSource wraps a DataSource with PII redaction.
// Defaults to PIIModeFull (no redaction) if piiMode is empty.
func NewRedactingDataSource(source DataSource, piiMode PIIMode) *RedactingDataSource {
	if piiMode == "" {
		piiMode = PIIModeFull
	}
	return &RedactingDataSource{source: source, piiMode: piiMode}
}

func (r *RedactingDataSource) Load(ctx context.Context) (io.ReadCloser, error) {
	reader, err := r.source.Load(ctx)
	if err != nil {
		return nil, err
	}

	if r.piiMode == PIIModeFull {
		return reader, nil
	}

	defer reader.Close()

	var data Data
	if err := json.NewDecoder(reader).Decode(&data); err != nil {
		return nil, fmt.Errorf("redacting data source: decode: %w", err)
	}

	redactPII(&data)

	out, err := json.Marshal(&data)
	if err != nil {
		return nil, fmt.Errorf("redacting data source: encode: %w", err)
	}
	return io.NopCloser(bytes.NewReader(out)), nil
}

func (r *RedactingDataSource) Watch(ctx context.Context, callback func() error) error {
	return r.source.Watch(ctx, callback)
}

func (r *RedactingDataSource) String() string {
	if r.piiMode == PIIModeRedacted {
		return fmt.Sprintf("%s [PII redacted]", r.source)
	}
	return r.source.String()
}

func (r *RedactingDataSource) Close() error {
	return r.source.Close()
}

func redactPII(data *Data) {
	for uid, emp := range data.Lookups.Employees {
		emp.FullName = "[REDACTED]"
		emp.Email = "[REDACTED]"
		emp.SlackUID = ""
		emp.GitHubID = ""
		data.Lookups.Employees[uid] = emp
	}

	data.Indexes.SlackIDMappings.SlackUIDToUID = map[string]string{}
	data.Indexes.GitHubIDMappings.GitHubIDToUID = map[string]string{}
}
