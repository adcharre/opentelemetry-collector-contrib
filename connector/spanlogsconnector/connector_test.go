// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package spanlogsconnector

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/connector/connectortest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/open-telemetry/opentelemetry-collector-contrib/connector/spanlogsconnector/internal/metadata"
)

var (
	testTraceID = pcommon.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	testSpanID  = pcommon.SpanID{1, 2, 3, 4, 5, 6, 7, 8}
	testStart   = time.Unix(1700000000, 0)
)

func testTraces() ptrace.Traces {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.SetSchemaUrl("https://opentelemetry.io/schemas/1.26.0")
	rs.Resource().Attributes().PutStr("service.name", "checkout")
	rs.Resource().Attributes().PutStr("host.name", "host-1")

	ss := rs.ScopeSpans().AppendEmpty()
	ss.Scope().SetName("test-scope")
	ss.Scope().SetVersion("1.0.0")

	span := ss.Spans().AppendEmpty()
	span.SetName("GET /cart")
	span.SetTraceID(testTraceID)
	span.SetSpanID(testSpanID)
	span.SetFlags(0x101) // sampled + remote parent bit
	span.SetStartTimestamp(pcommon.NewTimestampFromTime(testStart))
	span.SetEndTimestamp(pcommon.NewTimestampFromTime(testStart.Add(250 * time.Millisecond)))
	span.Attributes().PutStr("http.route", "/cart")
	span.Attributes().PutInt("http.response.status_code", 500)
	span.Attributes().PutStr("user.id", "secret")

	event := span.Events().AppendEmpty()
	event.SetName("exception")
	event.SetTimestamp(pcommon.NewTimestampFromTime(testStart.Add(100 * time.Millisecond)))
	event.Attributes().PutStr("exception.type", "IOError")
	event.Attributes().PutStr("exception.message", "disk full")
	return td
}

func consume(t *testing.T, cfg *Config, td ptrace.Traces) []plog.Logs {
	t.Helper()
	require.NoError(t, cfg.Validate())
	sink := &consumertest.LogsSink{}
	conn, err := NewFactory().CreateTracesToLogs(t.Context(), connectortest.NewNopSettings(metadata.Type), cfg, sink)
	require.NoError(t, err)
	require.NoError(t, conn.Start(t.Context(), nil))
	require.NoError(t, conn.ConsumeTraces(t.Context(), td))
	require.NoError(t, conn.Shutdown(t.Context()))
	return sink.AllLogs()
}

func TestConsumeTracesDefaultConfig(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	logs := consume(t, cfg, testTraces())
	require.Len(t, logs, 1)
	require.Equal(t, 1, logs[0].LogRecordCount())

	rl := logs[0].ResourceLogs().At(0)
	assert.Equal(t, "https://opentelemetry.io/schemas/1.26.0", rl.SchemaUrl())
	assert.Equal(t, map[string]any{"service.name": "checkout"}, rl.Resource().Attributes().AsRaw())

	sl := rl.ScopeLogs().At(0)
	assert.Equal(t, "test-scope", sl.Scope().Name())
	assert.Equal(t, "1.0.0", sl.Scope().Version())

	lr := sl.LogRecords().At(0)
	assert.Equal(t, pcommon.NewTimestampFromTime(testStart), lr.Timestamp())
	assert.NotZero(t, lr.ObservedTimestamp())
	assert.Equal(t, testTraceID, lr.TraceID())
	assert.Equal(t, testSpanID, lr.SpanID())
	assert.Equal(t, plog.LogRecordFlags(0x01), lr.Flags())
	assert.Empty(t, lr.EventName())
	assert.Equal(t, map[string]any{attrSpanDuration: int64(250 * time.Millisecond)}, lr.Attributes().AsRaw())
	assert.Equal(t, `span.name="GET /cart" span.duration=250ms`, lr.Body().Str())
}

func TestConsumeTracesSpanAttributesAndTemplate(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.ResourceAttributes = []string{"service.name", "host.name", "missing"}
	cfg.Spans.Attributes = []string{"http.route", "http.response.status_code", "missing"}
	cfg.Spans.Body = BodyConfig{Template: "{service.name}: {span.name} -> {http.response.status_code}"}

	logs := consume(t, cfg, testTraces())
	require.Len(t, logs, 1)

	rl := logs[0].ResourceLogs().At(0)
	assert.Equal(t, map[string]any{"service.name": "checkout", "host.name": "host-1"}, rl.Resource().Attributes().AsRaw())

	lr := rl.ScopeLogs().At(0).LogRecords().At(0)
	assert.Equal(t, map[string]any{
		attrSpanDuration:            int64(250 * time.Millisecond),
		"http.route":                "/cart",
		"http.response.status_code": int64(500),
	}, lr.Attributes().AsRaw())
	assert.Equal(t, "checkout: GET /cart -> 500", lr.Body().Str())
}

func TestConsumeTracesSpanEvents(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.SpanEvents.Enabled = true
	cfg.SpanEvents.Attributes = []string{"exception.type", "http.route"}

	logs := consume(t, cfg, testTraces())
	require.Len(t, logs, 1)
	records := logs[0].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
	require.Equal(t, 2, records.Len())

	spanRecord := records.At(0)
	assert.Equal(t, pcommon.NewTimestampFromTime(testStart), spanRecord.Timestamp())

	eventRecord := records.At(1)
	assert.Equal(t, pcommon.NewTimestampFromTime(testStart.Add(100*time.Millisecond)), eventRecord.Timestamp())
	assert.Equal(t, "exception", eventRecord.EventName())
	assert.Equal(t, testTraceID, eventRecord.TraceID())
	assert.Equal(t, testSpanID, eventRecord.SpanID())
	assert.Equal(t, map[string]any{
		attrSpanDuration: int64(250 * time.Millisecond),
		"exception.type": "IOError",
		"http.route":     "/cart",
	}, eventRecord.Attributes().AsRaw())
	assert.Equal(t, `event.name=exception span.name="GET /cart"`, eventRecord.Body().Str())
}

func TestConsumeTracesSpanEventsOnly(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Spans.Enabled = false
	cfg.SpanEvents.Enabled = true
	cfg.SpanEvents.Body = BodyConfig{Logfmt: []string{"exception.type", "exception.message"}}

	logs := consume(t, cfg, testTraces())
	require.Len(t, logs, 1)
	require.Equal(t, 1, logs[0].LogRecordCount())
	lr := logs[0].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	assert.Equal(t, `exception.type=IOError exception.message="disk full"`, lr.Body().Str())
}

func TestConsumeTracesNoRecords(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Spans.Enabled = false
	cfg.SpanEvents.Enabled = true

	// Spans without events produce no log records, so nothing is forwarded.
	td := testTraces()
	td.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Events().RemoveIf(func(ptrace.SpanEvent) bool { return true })
	assert.Empty(t, consume(t, cfg, td))
	assert.Empty(t, consume(t, cfg, ptrace.NewTraces()))
}

func TestConsumeTracesMultipleResources(t *testing.T) {
	td := testTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "payments")
	rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("charge")
	// A resource with no spans produces no resource logs.
	td.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty()

	logs := consume(t, createDefaultConfig().(*Config), td)
	require.Len(t, logs, 1)
	require.Equal(t, 2, logs[0].ResourceLogs().Len())
	second := logs[0].ResourceLogs().At(1)
	assert.Equal(t, map[string]any{"service.name": "payments"}, second.Resource().Attributes().AsRaw())
	assert.Equal(t, "span.name=charge span.duration=0s", second.ScopeLogs().At(0).LogRecords().At(0).Body().Str())
}

func TestConsumeTracesDoesNotMutateInput(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.SpanEvents.Enabled = true
	td := testTraces()
	expected := ptrace.NewTraces()
	td.CopyTo(expected)
	consume(t, cfg, td)
	assert.Equal(t, expected, td)
}

func TestConsumeTracesPropagatesError(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	wantErr := errors.New("downstream failure")
	conn, err := NewFactory().CreateTracesToLogs(t.Context(), connectortest.NewNopSettings(metadata.Type), cfg, consumertest.NewErr(wantErr))
	require.NoError(t, err)
	assert.False(t, conn.Capabilities().MutatesData)
	assert.ErrorIs(t, conn.ConsumeTraces(t.Context(), testTraces()), wantErr)
}

func TestNewSpanLogsInvalidTemplate(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Spans.Body.Template = "{"
	_, err := newSpanLogs(cfg, consumertest.NewNop())
	assert.ErrorContains(t, err, "unclosed")

	cfg = createDefaultConfig().(*Config)
	cfg.SpanEvents.Body.Template = "}"
	_, err = newSpanLogs(cfg, consumertest.NewNop())
	assert.ErrorContains(t, err, "unexpected")
}
