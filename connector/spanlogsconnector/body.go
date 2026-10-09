// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package spanlogsconnector // import "github.com/open-telemetry/opentelemetry-collector-contrib/connector/spanlogsconnector"

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// Built-in fields that can be referenced from a body template or logfmt field list.
const (
	fieldSpanName     = "span.name"
	fieldSpanKind     = "span.kind"
	fieldSpanStatus   = "span.status"
	fieldSpanDuration = "span.duration"
	fieldTraceID      = "trace_id"
	fieldSpanID       = "span_id"
	fieldEventName    = "event.name"
)

var (
	defaultSpanLogfmtFields  = []string{fieldSpanName, fieldSpanDuration}
	defaultEventLogfmtFields = []string{fieldEventName, fieldSpanName}
)

// recordContext holds the telemetry that a log record is built from. event is
// only set when building a log record for a span event.
type recordContext struct {
	resource pcommon.Resource
	span     ptrace.Span
	event    ptrace.SpanEvent
	isEvent  bool
}

// lookup resolves a field key to its string value. Built-in fields are checked
// first, followed by span event attributes (for span event records), span
// attributes and finally resource attributes.
func (rc *recordContext) lookup(key string) (string, bool) {
	switch key {
	case fieldSpanName:
		return rc.span.Name(), true
	case fieldSpanKind:
		return rc.span.Kind().String(), true
	case fieldSpanStatus:
		return rc.span.Status().Code().String(), true
	case fieldSpanDuration:
		return spanDuration(rc.span).String(), true
	case fieldTraceID:
		return rc.span.TraceID().String(), true
	case fieldSpanID:
		return rc.span.SpanID().String(), true
	case fieldEventName:
		if rc.isEvent {
			return rc.event.Name(), true
		}
		return "", false
	}
	if rc.isEvent {
		if v, ok := rc.event.Attributes().Get(key); ok {
			return v.AsString(), true
		}
	}
	if v, ok := rc.span.Attributes().Get(key); ok {
		return v.AsString(), true
	}
	if v, ok := rc.resource.Attributes().Get(key); ok {
		return v.AsString(), true
	}
	return "", false
}

func spanDuration(span ptrace.Span) time.Duration {
	start, end := span.StartTimestamp(), span.EndTimestamp()
	if end < start {
		return 0
	}
	return time.Duration(end - start)
}

// bodyBuilder renders the body of a log record.
type bodyBuilder interface {
	build(rc *recordContext) string
}

func newBodyBuilder(cfg BodyConfig, defaultFields []string) (bodyBuilder, error) {
	if cfg.Template != "" {
		return parseTemplate(cfg.Template)
	}
	if len(cfg.Logfmt) > 0 {
		return logfmtBuilder(cfg.Logfmt), nil
	}
	return logfmtBuilder(defaultFields), nil
}

// templateSegment is either literal text or, when isField is set, a field key.
type templateSegment struct {
	text    string
	isField bool
}

type templateBuilder []templateSegment

func parseTemplate(tmpl string) (templateBuilder, error) {
	var (
		segments templateBuilder
		literal  strings.Builder
	)
	for i := 0; i < len(tmpl); i++ {
		c := tmpl[i]
		switch {
		case c == '{' && i+1 < len(tmpl) && tmpl[i+1] == '{':
			literal.WriteByte('{')
			i++
		case c == '}' && i+1 < len(tmpl) && tmpl[i+1] == '}':
			literal.WriteByte('}')
			i++
		case c == '{':
			end := strings.IndexByte(tmpl[i+1:], '}')
			if end < 0 {
				return nil, fmt.Errorf("unclosed '{' at position %d in template", i)
			}
			key := strings.TrimSpace(tmpl[i+1 : i+1+end])
			if key == "" {
				return nil, fmt.Errorf("empty field name at position %d in template", i)
			}
			if literal.Len() > 0 {
				segments = append(segments, templateSegment{text: literal.String()})
				literal.Reset()
			}
			segments = append(segments, templateSegment{text: key, isField: true})
			i += end + 1
		case c == '}':
			return nil, fmt.Errorf("unexpected '}' at position %d in template, use '}}' for a literal brace", i)
		default:
			literal.WriteByte(c)
		}
	}
	if literal.Len() > 0 {
		segments = append(segments, templateSegment{text: literal.String()})
	}
	if len(segments) == 0 {
		return nil, errors.New("template is empty")
	}
	return segments, nil
}

func (t templateBuilder) build(rc *recordContext) string {
	var sb strings.Builder
	for _, seg := range t {
		if !seg.isField {
			sb.WriteString(seg.text)
			continue
		}
		// Missing fields render as an empty string.
		v, _ := rc.lookup(seg.text)
		sb.WriteString(v)
	}
	return sb.String()
}

type logfmtBuilder []string

func (l logfmtBuilder) build(rc *recordContext) string {
	var sb strings.Builder
	for _, key := range l {
		v, ok := rc.lookup(key)
		if !ok {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(key)
		sb.WriteByte('=')
		writeLogfmtValue(&sb, v)
	}
	return sb.String()
}

func writeLogfmtValue(sb *strings.Builder, v string) {
	if v != "" && !strings.ContainsAny(v, " =\"\\\t\n\r") {
		sb.WriteString(v)
		return
	}
	sb.WriteByte('"')
	for _, r := range v {
		switch r {
		case '"', '\\':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
}
