// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awss3receiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awss3receiver"

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/open-telemetry/opamp-go/client/types"
	"github.com/open-telemetry/opamp-go/protobufs"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/opampcustommessages"
)

type mockCustomCapabilityRegistry struct {
	component.Component

	shouldFailRegister  bool
	shouldReturnPending func() bool
	shouldFailSend      bool

	sendMessageCalls int

	pendingChannel   chan struct{}
	unregisterCalled bool
	sentMessages     []customMessage
}

type customMessage struct {
	messageType string
	message     []byte
}

type hostWithCustomCapabilityRegistry struct {
	extension *mockCustomCapabilityRegistry
}

func (h hostWithCustomCapabilityRegistry) Start(context.Context, component.Host) error {
	panic("unsupported")
}

func (h hostWithCustomCapabilityRegistry) Shutdown(context.Context) error {
	panic("unsupported")
}

func (h hostWithCustomCapabilityRegistry) GetFactory(_ component.Kind, _ component.Type) component.Factory {
	panic("unsupported")
}

func (h hostWithCustomCapabilityRegistry) GetExtensions() map[component.ID]component.Component {
	return map[component.ID]component.Component{
		component.MustNewID("foo"): h.extension,
	}
}

func (h hostWithCustomCapabilityRegistry) GetExporters() map[component.DataType]map[component.ID]component.Component {
	panic("unsupported")
}

func (m *mockCustomCapabilityRegistry) Register(_ string, _ ...opampcustommessages.CustomCapabilityRegisterOption) (handler opampcustommessages.CustomCapabilityHandler, err error) {
	if m.shouldFailRegister {
		return nil, fmt.Errorf("register failed")
	}
	return m, nil
}

func (m *mockCustomCapabilityRegistry) Message() <-chan *protobufs.CustomMessage {
	panic("unsupported")
}

func (m *mockCustomCapabilityRegistry) SendMessage(messageType string, message []byte) (messageSendingChannel chan struct{}, err error) {
	m.sendMessageCalls++
	if m.unregisterCalled {
		return nil, fmt.Errorf("unregister called")
	}
	if m.shouldReturnPending != nil && m.shouldReturnPending() {
		return m.pendingChannel, types.ErrCustomMessagePending
	}
	if m.shouldFailSend {
		return nil, fmt.Errorf("send failed")
	}
	m.sentMessages = append(m.sentMessages, customMessage{messageType: messageType, message: message})
	return nil, nil
}

func (m *mockCustomCapabilityRegistry) Unregister() {
	m.unregisterCalled = true
}

func Test_opampNotifier_Start(t *testing.T) {
	id := component.MustNewID("foo")

	tests := []struct {
		name    string
		host    component.Host
		wantErr bool
	}{
		{
			name: "success",
			host: hostWithCustomCapabilityRegistry{
				extension: &mockCustomCapabilityRegistry{},
			},
			wantErr: false,
		},
		{
			name:    "extension not found",
			host:    componenttest.NewNopHost(),
			wantErr: true,
		},
		{
			name: "register failed",
			host: hostWithCustomCapabilityRegistry{
				extension: &mockCustomCapabilityRegistry{
					shouldFailRegister: true,
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notifier := &opampNotifier{opampExtensionID: id}
			err := notifier.Start(context.Background(), tt.host)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func Test_opampNotifier_Shutdown(t *testing.T) {
	registry := mockCustomCapabilityRegistry{}
	notifier := &opampNotifier{handler: &registry, logger: zap.NewNop()}
	err := notifier.Shutdown(context.Background())
	require.NoError(t, err)
	require.True(t, registry.unregisterCalled)
}

func Test_opampNotifier_SendStatus(t *testing.T) {
	registry := mockCustomCapabilityRegistry{}
	notifier := &opampNotifier{handler: &registry, logger: zap.NewNop()}
	ingestTime := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	toSend := statusNotification{
		TelemetryType: "telemetry",
		IngestStatus:  IngestStatusIngesting,
		IngestTime:    ingestTime,
		StartTime:     ingestTime,
		EndTime:       ingestTime,
	}
	notifier.SendStatus(context.Background(), toSend)
	require.Len(t, registry.sentMessages, 1)
	require.Equal(t, "TimeBasedIngestStatus", registry.sentMessages[0].messageType)

	unmarshaler := plog.ProtoUnmarshaler{}
	logs, err := unmarshaler.UnmarshalLogs(registry.sentMessages[0].message)
	require.NoError(t, err)
	require.Equal(t, logs.ResourceLogs().Len(), 1)
	require.Equal(t, logs.ResourceLogs().At(0).ScopeLogs().Len(), 1)
	require.Equal(t, logs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().Len(), 1)
	log := logs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	require.Equal(t, log.Body().Str(), "status")
	attr := log.Attributes()
	v, b := attr.Get("telemetry_type")
	require.True(t, b)
	require.Equal(t, v.Str(), "telemetry")

	v, b = attr.Get("ingest_status")
	require.True(t, b)
	require.Equal(t, v.Str(), IngestStatusIngesting)

	require.Equal(t, log.Timestamp(), pcommon.NewTimestampFromTime(ingestTime))

	v, b = attr.Get("start_time")
	require.True(t, b)
	require.Equal(t, v.Int(), int64(pcommon.NewTimestampFromTime(ingestTime)))

	v, b = attr.Get("end_time")
	require.True(t, b)
	require.Equal(t, v.Int(), int64(pcommon.NewTimestampFromTime(ingestTime)))

	_, b = attr.Get("failure_message")
	require.False(t, b)
}

func Test_opampNotifier_SendStatus_MessagePending(t *testing.T) {
	tryCount := 0
	registry := mockCustomCapabilityRegistry{
		shouldReturnPending: func() bool {
			pending := tryCount < 1
			tryCount++
			return pending
		},
		pendingChannel: make(chan struct{}),
	}
	notifier := &opampNotifier{handler: &registry, logger: zap.NewNop()}
	toSend := statusNotification{
		TelemetryType: "telemetry",
		IngestStatus:  IngestStatusIngesting,
		IngestTime:    time.Time{},
	}

	var completionTime time.Time

	now := time.Now()
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		notifier.SendStatus(context.Background(), toSend)
		completionTime = time.Now()
		wg.Done()
	}()
	require.Empty(t, registry.sentMessages)
	registry.pendingChannel <- struct{}{}
	wg.Wait()
	require.True(t, completionTime.After(now))
	require.Len(t, registry.sentMessages, 1)
	require.Equal(t, "TimeBasedIngestStatus", registry.sentMessages[0].messageType)
}

func Test_opampNotifier_SendStatus_Error(t *testing.T) {
	registry := mockCustomCapabilityRegistry{
		shouldFailSend: true,
	}
	notifier := &opampNotifier{handler: &registry, logger: zap.NewNop()}
	toSend := statusNotification{
		TelemetryType: "telemetry",
		IngestStatus:  IngestStatusIngesting,
		IngestTime:    time.Time{},
	}

	notifier.SendStatus(context.Background(), toSend)
	require.Empty(t, registry.sentMessages)
	require.Equal(t, registry.sendMessageCalls, 1)
}

func Test_opampNotifier_SendStatus_MaxRetries(t *testing.T) {
	registry := mockCustomCapabilityRegistry{
		shouldReturnPending: func() bool { return true },
		pendingChannel:      make(chan struct{}),
	}
	notifier := &opampNotifier{handler: &registry, logger: zap.NewNop()}
	toSend := statusNotification{
		TelemetryType: "telemetry",
		IngestStatus:  IngestStatusIngesting,
		IngestTime:    time.Time{},
	}
	var completionTime time.Time
	now := time.Now()
	wg := &sync.WaitGroup{}
	wg.Add(1)

	go func() {
		notifier.SendStatus(context.Background(), toSend)
		completionTime = time.Now()
		wg.Done()
	}()

	for attempt := 0; attempt < maxNotificationAttempts; attempt++ {
		registry.pendingChannel <- struct{}{}
	}
	wg.Wait()

	require.True(t, completionTime.After(now))
	require.Empty(t, registry.sentMessages)
	require.Equal(t, registry.sendMessageCalls, maxNotificationAttempts)
}
