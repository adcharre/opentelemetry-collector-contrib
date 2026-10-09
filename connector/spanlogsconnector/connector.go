// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package spanlogsconnector // import "github.com/open-telemetry/opentelemetry-collector-contrib/connector/spanlogsconnector"

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// attrSpanDuration is added to every produced log record and holds the span
// duration (end time - start time) in nanoseconds.
const attrSpanDuration = "span.duration_ns"

const traceFlagsMask = 0xff

type spanLogs struct {
	logsConsumer consumer.Logs
	config       *Config
	spanBody     bodyBuilder
	eventBody    bodyBuilder
	component.StartFunc
	component.ShutdownFunc
}

func newSpanLogs(cfg *Config, next consumer.Logs) (*spanLogs, error) {
	spanBody, err := newBodyBuilder(cfg.Spans.Body, defaultSpanLogfmtFields)
	if err != nil {
		return nil, err
	}
	eventBody, err := newBodyBuilder(cfg.SpanEvents.Body, defaultEventLogfmtFields)
	if err != nil {
		return nil, err
	}
	return &spanLogs{
		logsConsumer: next,
		config:       cfg,
		spanBody:     spanBody,
		eventBody:    eventBody,
	}, nil
}

func (*spanLogs) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

func (c *spanLogs) ConsumeTraces(ctx context.Context, td ptrace.Traces) error {
	logs := plog.NewLogs()
	observed := pcommon.NewTimestampFromTime(time.Now())

	for i := 0; i < td.ResourceSpans().Len(); i++ {
		rs := td.ResourceSpans().At(i)
		rl := plog.NewResourceLogs()
		copyAttributes(rs.Resource().Attributes(), rl.Resource().Attributes(), c.config.ResourceAttributes)

		for j := 0; j < rs.ScopeSpans().Len(); j++ {
			ss := rs.ScopeSpans().At(j)
			sl := plog.NewScopeLogs()
			ss.Scope().CopyTo(sl.Scope())
			sl.SetSchemaUrl(ss.SchemaUrl())

			for k := 0; k < ss.Spans().Len(); k++ {
				c.appendSpanRecords(rs.Resource(), ss.Spans().At(k), sl.LogRecords(), observed)
			}
			if sl.LogRecords().Len() > 0 {
				sl.MoveTo(rl.ScopeLogs().AppendEmpty())
			}
		}
		if rl.ScopeLogs().Len() > 0 {
			rl.SetSchemaUrl(rs.SchemaUrl())
			rl.MoveTo(logs.ResourceLogs().AppendEmpty())
		}
	}

	if logs.ResourceLogs().Len() == 0 {
		return nil
	}
	return c.logsConsumer.ConsumeLogs(ctx, logs)
}

func (c *spanLogs) appendSpanRecords(res pcommon.Resource, span ptrace.Span, records plog.LogRecordSlice, observed pcommon.Timestamp) {
	rc := &recordContext{resource: res, span: span}
	duration := int64(spanDuration(span))

	if c.config.Spans.Enabled {
		lr := records.AppendEmpty()
		setCommonFields(lr, span, observed, duration)
		lr.SetTimestamp(span.StartTimestamp())
		copyAttributes(span.Attributes(), lr.Attributes(), c.config.Spans.Attributes)
		lr.Body().SetStr(c.spanBody.build(rc))
	}

	if !c.config.SpanEvents.Enabled {
		return
	}
	rc.isEvent = true
	for i := 0; i < span.Events().Len(); i++ {
		event := span.Events().At(i)
		rc.event = event

		lr := records.AppendEmpty()
		setCommonFields(lr, span, observed, duration)
		lr.SetTimestamp(event.Timestamp())
		lr.SetEventName(event.Name())
		copyEventAttributes(event, span, lr.Attributes(), c.config.SpanEvents.Attributes)
		lr.Body().SetStr(c.eventBody.build(rc))
	}
}

func setCommonFields(lr plog.LogRecord, span ptrace.Span, observed pcommon.Timestamp, duration int64) {
	lr.SetObservedTimestamp(observed)
	lr.SetTraceID(span.TraceID())
	lr.SetSpanID(span.SpanID())
	// The lower 8 bits of both span and log record flags hold the W3C trace flags.
	lr.SetFlags(plog.LogRecordFlags(span.Flags() & traceFlagsMask))
	lr.Attributes().PutInt(attrSpanDuration, duration)
}

// copyAttributes copies the listed keys from src to dst, skipping keys that are not present.
func copyAttributes(src, dst pcommon.Map, keys []string) {
	for _, key := range keys {
		if v, ok := src.Get(key); ok {
			v.CopyTo(dst.PutEmpty(key))
		}
	}
}

// copyEventAttributes copies the listed keys onto dst, preferring the span
// event's attributes and falling back to the parent span's attributes.
func copyEventAttributes(event ptrace.SpanEvent, span ptrace.Span, dst pcommon.Map, keys []string) {
	for _, key := range keys {
		v, ok := event.Attributes().Get(key)
		if !ok {
			v, ok = span.Attributes().Get(key)
		}
		if ok {
			v.CopyTo(dst.PutEmpty(key))
		}
	}
}
