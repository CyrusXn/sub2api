//go:build unit

package service

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestAccountTextProbeRequiresRealOutputAndTerminal(t *testing.T) {
	for _, tt := range []struct {
		name string
		chat bool
		body string
		ok   bool
	}{
		{name: "responses empty completed", body: `data: {"type":"response.completed"}`},
		{name: "responses whitespace completed", body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\" \\n\"}\n\ndata: {\"type\":\"response.completed\"}"},
		{name: "responses metadata completed", body: "data: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.completed\"}"},
		{name: "responses real EOF", body: `data: {"type":"response.output_text.delta","delta":"hello"}`},
		{name: "responses real failed", body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.failed\"}"},
		{name: "responses real incomplete status", body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"incomplete\"}}"},
		{name: "responses real completed", body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\"}", ok: true},
		{name: "responses completed carries real text", body: `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}]}}`, ok: true},
		{name: "responses completed carries error", body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"error\":{\"message\":\"failed\"}}}"},
		{name: "chat metadata DONE", chat: true, body: "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\ndata: [DONE]"},
		{name: "chat whitespace finish DONE", chat: true, body: "data: {\"choices\":[{\"delta\":{\"content\":\" \"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]"},
		{name: "chat real EOF", chat: true, body: `data: {"choices":[{"delta":{"content":"hello"}}]}`},
		{name: "chat real DONE", chat: true, body: "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]", ok: true},
		{name: "chat real finish EOF", chat: true, body: `data: {"choices":[{"delta":{"content":"hello"},"finish_reason":"stop"}]}`, ok: true},
		{name: "chat unknown finish EOF", chat: true, body: `data: {"choices":[{"delta":{"content":"hello"},"finish_reason":"failed"}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newTestContext()
			svc := &AccountTestService{}
			var err error
			if tt.chat {
				err = svc.processOpenAIChatCompletionsStream(c, strings.NewReader(tt.body+"\n\n"))
			} else {
				err = svc.processOpenAIStream(c, strings.NewReader(tt.body+"\n\n"))
			}
			if tt.ok {
				require.NoError(t, err)
				require.Contains(t, rec.Body.String(), `"success":true`)
			} else {
				require.Error(t, err)
				require.NotContains(t, rec.Body.String(), `"success":true`)
			}
		})
	}
}

type accountTextDelayedUpstream struct {
	headerDelay time.Duration
	bodyDelay   time.Duration
}

func (u accountTextDelayedUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	time.Sleep(u.headerDelay)
	reader, writer := io.Pipe()
	go func() {
		defer writer.Close()
		time.Sleep(u.bodyDelay)
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\" \"}\n\n"+
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"\\n\"}\n\n"+
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"+
			"data: {\"type\":\"response.completed\"}\n\n")
	}()
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}, nil
}

func (u accountTextDelayedUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestAccountTextProbePlaceholderCoversHeadersAndBody(t *testing.T) {
	for _, header := range []bool{false, true} {
		name := "body"
		upstream := accountTextDelayedUpstream{bodyDelay: 1200 * time.Millisecond}
		if header {
			name = "headers"
			upstream = accountTextDelayedUpstream{headerDelay: 1200 * time.Millisecond}
		}
		t.Run(name, func(t *testing.T) {
			c, rec := newTestContext()
			svc := &AccountTestService{httpUpstream: upstream}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "test-token"}}
			err := svc.testOpenAIAccountConnection(c, account, "gpt-test", "hi", AccountTestModeDefault)
			require.NoError(t, err)
			require.NotNil(t, TextStreamFirstPacketMs(c))
			require.Less(t, *TextStreamFirstPacketMs(c), 1200)
			require.Equal(t, 1, strings.Count(rec.Body.String(), `"text":" "`))
			require.NotContains(t, rec.Body.String(), `"text":"\n"`)
			require.Contains(t, rec.Body.String(), "hello")
		})
	}
}

func TestAccountTextProbeTerminalStopsPlaceholder(t *testing.T) {
	for _, event := range []TestEvent{{Type: "error", Error: "failed"}, {Type: "test_complete", Success: true}} {
		t.Run(event.Type, func(t *testing.T) {
			c, rec := newTestContext()
			ensureTextStreamTiming(c, time.Now().Add(-950*time.Millisecond))
			stop := startAccountTextProbePlaceholder(c)
			defer stop()
			(&AccountTestService{}).sendEvent(c, event)
			time.Sleep(100 * time.Millisecond)
			stop()
			require.NotContains(t, rec.Body.String(), `"type":"content"`, "终态写出后不得再补占位")
		})
	}
}
