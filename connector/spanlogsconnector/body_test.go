// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package spanlogsconnector

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func testRecordContext() *recordContext {
	res := pcommon.NewResource()
	res.Attributes().PutStr("service.name", "checkout")

	span := ptrace.NewSpan()
	span.SetName("GET /cart")
	span.SetKind(ptrace.SpanKindServer)
	span.Status().SetCode(ptrace.StatusCodeError)
	span.SetTraceID(pcommon.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	span.SetSpanID(pcommon.SpanID{1, 2, 3, 4, 5, 6, 7, 8})
	start := time.Unix(1700000000, 0)
	span.SetStartTimestamp(pcommon.NewTimestampFromTime(start))
	span.SetEndTimestamp(pcommon.NewTimestampFromTime(start.Add(1500 * time.Millisecond)))
	span.Attributes().PutStr("http.route", "/cart")
	span.Attributes().PutInt("http.response.status_code", 500)
	span.Attributes().PutStr("shadowed", "span")

	event := span.Events().AppendEmpty()
	event.SetName("exception")
	event.Attributes().PutStr("exception.message", `bad "input"`)
	event.Attributes().PutStr("shadowed", "event")

	return &recordContext{resource: res, span: span, event: event}
}

func TestLookup(t *testing.T) {
	rc := testRecordContext()

	tests := []struct {
		key     string
		isEvent bool
		want    string
		wantOK  bool
	}{
		{key: fieldSpanName, want: "GET /cart", wantOK: true},
		{key: fieldSpanKind, want: "Server", wantOK: true},
		{key: fieldSpanStatus, want: "Error", wantOK: true},
		{key: fieldSpanDuration, want: "1.5s", wantOK: true},
		{key: fieldTraceID, want: "0102030405060708090a0b0c0d0e0f10", wantOK: true},
		{key: fieldSpanID, want: "0102030405060708", wantOK: true},
		{key: fieldEventName, wantOK: false},
		{key: fieldEventName, isEvent: true, want: "exception", wantOK: true},
		{key: "http.response.status_code", want: "500", wantOK: true},
		{key: "service.name", want: "checkout", wantOK: true},
		{key: "shadowed", want: "span", wantOK: true},
		{key: "shadowed", isEvent: true, want: "event", wantOK: true},
		{key: "exception.message", wantOK: false},
		{key: "missing", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			rc.isEvent = tt.isEvent
			got, ok := rc.lookup(tt.key)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSpanDurationNegative(t *testing.T) {
	span := ptrace.NewSpan()
	span.SetStartTimestamp(10)
	span.SetEndTimestamp(5)
	assert.Equal(t, time.Duration(0), spanDuration(span))
}

func TestTemplate(t *testing.T) {
	rc := testRecordContext()

	tests := []struct {
		name    string
		tmpl    string
		want    string
		wantErr string
	}{
		{name: "fields", tmpl: "{span.name} returned {http.response.status_code} in {span.duration}", want: "GET /cart returned 500 in 1.5s"},
		{name: "missing field", tmpl: "[{missing}]", want: "[]"},
		{name: "escaped braces", tmpl: "{{literal}} {{{span.kind}}}", want: "{literal} {Server}"},
		{name: "whitespace in key", tmpl: "{ span.name }", want: "GET /cart"},
		{name: "unclosed", tmpl: "{span.name", wantErr: "unclosed '{'"},
		{name: "unexpected close", tmpl: "span}", wantErr: "unexpected '}'"},
		{name: "empty key", tmpl: "{}", wantErr: "empty field name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := parseTemplate(tt.tmpl)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, b.build(rc))
		})
	}
}

func TestLogfmt(t *testing.T) {
	rc := testRecordContext()
	rc.isEvent = true

	b := logfmtBuilder{fieldEventName, fieldSpanName, "missing", "exception.message", "http.route"}
	assert.Equal(t, `event.name=exception span.name="GET /cart" exception.message="bad \"input\"" http.route=/cart`, b.build(rc))
}

func TestWriteLogfmtValue(t *testing.T) {
	tests := map[string]string{
		"plain":       "plain",
		"":            `""`,
		"a b":         `"a b"`,
		"a=b":         `"a=b"`,
		`back\slash`:  `"back\\slash"`,
		"line\nbreak": `"line\nbreak"`,
		"tab\there":   `"tab\there"`,
		"cr\rhere":    `"cr\rhere"`,
	}
	for in, want := range tests {
		var sb strings.Builder
		writeLogfmtValue(&sb, in)
		assert.Equal(t, want, sb.String(), "input %q", in)
	}
}

func TestNewBodyBuilderDefaults(t *testing.T) {
	b, err := newBodyBuilder(BodyConfig{}, defaultSpanLogfmtFields)
	require.NoError(t, err)
	assert.Equal(t, `span.name="GET /cart" span.duration=1.5s`, b.build(testRecordContext()))
}
