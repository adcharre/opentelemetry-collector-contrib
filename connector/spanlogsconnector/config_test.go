// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package spanlogsconnector

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/confmap/confmaptest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/connector/spanlogsconnector/internal/metadata"
)

func TestLoadConfig(t *testing.T) {
	cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
	require.NoError(t, err)

	tests := []struct {
		id          component.ID
		expected    component.Config
		errContains string
	}{
		{
			id:       component.NewID(metadata.Type),
			expected: createDefaultConfig(),
		},
		{
			id: component.NewIDWithName(metadata.Type, "full"),
			expected: &Config{
				ResourceAttributes: []string{"service.name", "deployment.environment.name"},
				Spans: SpansConfig{
					Enabled:    true,
					Attributes: []string{"http.request.method", "http.route"},
					Body: BodyConfig{
						Template: "{span.name} {http.request.method} {http.route} took {span.duration}",
					},
				},
				SpanEvents: SpanEventsConfig{
					Enabled:    true,
					Attributes: []string{"exception.type", "http.route"},
					Body: BodyConfig{
						Logfmt: []string{"event.name", "exception.message"},
					},
				},
			},
		},
		{
			id:          component.NewIDWithName(metadata.Type, "disabled"),
			errContains: "at least one of spans.enabled or span_events.enabled must be true",
		},
		{
			id:          component.NewIDWithName(metadata.Type, "both_body"),
			errContains: "spans.body: only one of template or logfmt may be set",
		},
		{
			id:          component.NewIDWithName(metadata.Type, "bad_template"),
			errContains: "spans.body: unclosed '{'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.id.String(), func(t *testing.T) {
			cfg := NewFactory().CreateDefaultConfig()
			sub, err := cm.Sub(tt.id.String())
			require.NoError(t, err)
			require.NoError(t, sub.Unmarshal(cfg))

			err = confmap.Validate(cfg)
			if tt.errContains != "" {
				assert.ErrorContains(t, err, tt.errContains)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, cfg)
		})
	}
}
