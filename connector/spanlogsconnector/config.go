// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package spanlogsconnector // import "github.com/open-telemetry/opentelemetry-collector-contrib/connector/spanlogsconnector"

import (
	"errors"
	"fmt"

	"go.opentelemetry.io/collector/component"
)

// Config defines the configuration options for the span logs connector.
type Config struct {
	// ResourceAttributes is the list of resource attribute keys copied from the
	// span's resource onto the resource of the produced logs.
	ResourceAttributes []string `mapstructure:"resource_attributes"`

	// Spans configures the log records produced for each span.
	Spans SpansConfig `mapstructure:"spans"`

	// SpanEvents configures the log records produced for each span event.
	SpanEvents SpanEventsConfig `mapstructure:"span_events"`

	_ struct{}
}

// SpansConfig configures the log records produced for spans.
type SpansConfig struct {
	// Enabled controls whether a log record is produced for each span.
	Enabled bool `mapstructure:"enabled"`

	// Attributes is the list of span attribute keys copied onto the log record.
	Attributes []string `mapstructure:"attributes"`

	// Body configures the log record body.
	Body BodyConfig `mapstructure:"body"`

	_ struct{}
}

// SpanEventsConfig configures the log records produced for span events.
type SpanEventsConfig struct {
	// Enabled controls whether a log record is produced for each span event.
	Enabled bool `mapstructure:"enabled"`

	// Attributes is the list of attribute keys copied onto the log record. Keys are
	// looked up on the span event first and then on the parent span.
	Attributes []string `mapstructure:"attributes"`

	// Body configures the log record body.
	Body BodyConfig `mapstructure:"body"`

	_ struct{}
}

// BodyConfig configures how the log record body is built. At most one of
// Template or Logfmt may be set. When neither is set a logfmt body with default
// fields is used.
type BodyConfig struct {
	// Template is a string where `{key}` placeholders are replaced with the value
	// of the named field. Use `{{` and `}}` for literal braces.
	Template string `mapstructure:"template"`

	// Logfmt is the list of field keys rendered into the body as logfmt `key=value` pairs.
	Logfmt []string `mapstructure:"logfmt"`

	_ struct{}
}

func (cfg *Config) Validate() error {
	var errs error
	if !cfg.Spans.Enabled && !cfg.SpanEvents.Enabled {
		errs = errors.Join(errs, errors.New("at least one of spans.enabled or span_events.enabled must be true"))
	}
	if cfg.Spans.Enabled {
		if err := cfg.Spans.Body.validate(); err != nil {
			errs = errors.Join(errs, fmt.Errorf("spans.body: %w", err))
		}
	}
	if cfg.SpanEvents.Enabled {
		if err := cfg.SpanEvents.Body.validate(); err != nil {
			errs = errors.Join(errs, fmt.Errorf("span_events.body: %w", err))
		}
	}
	return errs
}

func (b *BodyConfig) validate() error {
	if b.Template != "" && len(b.Logfmt) > 0 {
		return errors.New("only one of template or logfmt may be set")
	}
	if b.Template != "" {
		_, err := parseTemplate(b.Template)
		return err
	}
	return nil
}

func createDefaultConfig() component.Config {
	return &Config{
		ResourceAttributes: []string{"service.name"},
		Spans: SpansConfig{
			Enabled: true,
		},
		SpanEvents: SpanEventsConfig{
			Enabled: false,
		},
	}
}
