package orgdatacore

import (
	"errors"
	"fmt"
)

var (
	ErrNoData                = errors.New("orgdatacore: no data loaded")
	ErrNotFound              = errors.New("orgdatacore: entity not found")
	ErrGCSNotEnabled         = errors.New("orgdatacore: GCS support not enabled - build with -tags gcs")
	ErrInvalidConfig         = errors.New("orgdatacore: invalid configuration")
	ErrWatcherAlreadyRunning = errors.New("orgdatacore: watcher already running")
	ErrInvalidData           = errors.New("orgdatacore: invalid data structure")
)

// NotFoundError wraps ErrNotFound with details about what wasn't found.
type NotFoundError struct {
	EntityType string
	Key        string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("orgdatacore: %s not found: %q", e.EntityType, e.Key)
}

func (e *NotFoundError) Is(target error) bool {
	return target == ErrNotFound
}

func (e *NotFoundError) Unwrap() error {
	return ErrNotFound
}

func NewNotFoundError(entityType, key string) *NotFoundError {
	return &NotFoundError{EntityType: entityType, Key: key}
}

// ConfigError wraps ErrInvalidConfig with field-specific details.
type ConfigError struct {
	Field   string
	Message string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("orgdatacore: invalid config for %s: %s", e.Field, e.Message)
}

func (e *ConfigError) Is(target error) bool {
	return target == ErrInvalidConfig
}

func (e *ConfigError) Unwrap() error {
	return ErrInvalidConfig
}

func NewConfigError(field, message string) *ConfigError {
	return &ConfigError{Field: field, Message: message}
}

// LoadError wraps data loading failures with source information.
type LoadError struct {
	Source string
	Err    error
}

func (e *LoadError) Error() string {
	return fmt.Sprintf("orgdatacore: failed to load from %s: %v", e.Source, e.Err)
}

func (e *LoadError) Unwrap() error {
	return e.Err
}

func NewLoadError(source string, err error) *LoadError {
	return &LoadError{Source: source, Err: err}
}
