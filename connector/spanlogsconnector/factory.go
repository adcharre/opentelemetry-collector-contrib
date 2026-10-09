// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package spanlogsconnector // import "github.com/open-telemetry/opentelemetry-collector-contrib/connector/spanlogsconnector"

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/connector"
	"go.opentelemetry.io/collector/consumer"

	"github.com/open-telemetry/opentelemetry-collector-contrib/connector/spanlogsconnector/internal/metadata"
)

// NewFactory returns a connector.Factory for the span logs connector.
func NewFactory() connector.Factory {
	return connector.NewFactory(
		metadata.Type,
		createDefaultConfig,
		connector.WithTracesToLogs(createTracesToLogs, metadata.TracesToLogsStability),
	)
}

func createTracesToLogs(
	_ context.Context,
	_ connector.Settings,
	cfg component.Config,
	nextConsumer consumer.Logs,
) (connector.Traces, error) {
	return newSpanLogs(cfg.(*Config), nextConsumer)
}
