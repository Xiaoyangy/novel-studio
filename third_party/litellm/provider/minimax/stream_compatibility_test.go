package minimax

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/provider/compat"
)

type streamFixtureClient func(*http.Request) (*http.Response, error)

func (f streamFixtureClient) Do(r *http.Request) (*http.Response, error) { return f(r) }

func fixtureChunk(index int, delta map[string]any, finish string) string {
	choice := map[string]any{"index": index, "delta": delta}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	raw, _ := json.Marshal(map[string]any{"choices": []any{choice}})
	return "data: " + string(raw) + "\n\n"
}

func fixtureText(value string) string   { return fixtureChunk(0, map[string]any{"content": value}, "") }
func fixtureFinish(value string) string { return fixtureChunk(0, map[string]any{}, value) }
func fixtureTool(id, name, arguments string) string {
	return fixtureChunk(0, map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "function": map[string]any{"name": name, "arguments": arguments}}}}, "")
}

func fixtureClient(body io.Reader) streamFixtureClient {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(body)}, nil
	}
}

func fixtureStream(t *testing.T, ctx context.Context, endpoint, model string, body io.Reader) litellm.Stream {
	t.Helper()
	p, err := New(Config{APIKey: "synthetic-test-key", BaseURL: endpoint, HTTPClient: fixtureClient(body)})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := p.Stream(ctx, &litellm.Request{Model: model, Messages: []litellm.Message{litellm.UserText("synthetic fixture")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	return stream
}

func TestStreamEndpointAndModelModesAreExplicit(t *testing.T) {
	for _, host := range []string{"api.minimax.cn", "api.minimaxi.com", "api.minimax.io", "proxy.invalid", "api.minimax.cn.invalid"} {
		for _, model := range []string{"MiniMax-M2", "MiniMax-M2.1", "MiniMax-M2.5", "MiniMax-M2.7-highspeed", "MiniMax-M3", "minimax-text-01"} {
			t.Run(host+"/"+model, func(t *testing.T) {
				delta := (host == "api.minimax.cn" || host == "api.minimaxi.com") && model != "minimax-text-01"
				var wire strings.Builder
				for i := 1; i <= 64; i++ {
					text, reasoning := "x", "r"
					if !delta {
						text, reasoning = strings.Repeat(text, i), strings.Repeat(reasoning, i)
					}
					wire.WriteString(fixtureChunk(0, map[string]any{"content": text, "reasoning_content": reasoning, "reasoning_details": []any{map[string]any{"type": "reasoning.text", "text": reasoning}}}, ""))
				}
				wire.WriteString(fixtureFinish("stop"))
				wire.WriteString("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":20,\"total_tokens\":30}}\n\n")
				stream := fixtureStream(t, context.Background(), "https://"+host+"/v1", model, strings.NewReader(wire.String()))
				response, err := litellm.Collect(stream)
				if err != nil {
					t.Fatal(err)
				}
				var thinking strings.Builder
				for _, block := range response.Blocks {
					if b, ok := block.(litellm.ReasoningBlock); ok {
						thinking.WriteString(b.Text)
					}
				}
				if response.Text() != strings.Repeat("x", 64) || thinking.String() != strings.Repeat("r", 64) || response.Usage.TotalTokens != 30 {
					t.Fatal("delta repetition/cumulative suffix or trailing usage was lost")
				}
				if _, err := stream.Next(); err != io.EOF {
					t.Fatalf("stream did not end once: %v", err)
				}
			})
		}
	}
}

func TestStreamModeUsesRequestedModelAndKeepsLegacyFieldPriority(t *testing.T) {
	wire := fixtureText("x") + "data: {\"model\":\"minimax-text-01\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n" + fixtureFinish("stop")
	response, err := litellm.Collect(fixtureStream(t, context.Background(), "https://api.minimax.cn/v1", "MiniMax-M3", strings.NewReader(wire)))
	if err != nil || response.Text() != "xx" {
		t.Fatalf("response model changed stream mode: %v", err)
	}
	wire = fixtureChunk(0, map[string]any{"reasoning_content": "alternate", "reasoning_details": []any{map[string]any{"type": "reasoning.text", "text": "legacy"}}, "content": "ok"}, "stop")
	response, err = litellm.Collect(fixtureStream(t, context.Background(), "https://api.minimax.io/v1", "MiniMax-M3", strings.NewReader(wire)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, block := range response.Blocks {
		if reasoning, ok := block.(litellm.ReasoningBlock); ok {
			found = true
			if reasoning.Text != "legacy" {
				t.Fatal("legacy reasoning field priority changed")
			}
		}
	}
	if !found {
		t.Fatal("legacy reasoning field disappeared")
	}
}

func TestStreamCompletedToolWaitsForValidatedEnd(t *testing.T) {
	for _, sentinel := range []string{"", "data: [DONE]\n\n"} {
		wire := fixtureTool("call_1", "lookup", `{"q":`) + fixtureTool("", "", `"ok"}`) + fixtureFinish("tool_calls") + sentinel
		stream := fixtureStream(t, context.Background(), "https://api.minimax.cn/v1", "MiniMax-M3", strings.NewReader(wire))
		toolDone, done := 0, 0
		for {
			event, err := stream.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			switch event.(type) {
			case litellm.ToolUseDone:
				toolDone++
			case litellm.DoneEvent:
				done++
			}
		}
		if toolDone != 1 || done != 1 {
			t.Fatalf("completion counts tool=%d done=%d", toolDone, done)
		}
	}
}

func TestStreamFailuresNeverPublishCompletedTools(t *testing.T) {
	tool := fixtureTool("call_1", "lookup", `{}`)
	finished := tool + fixtureFinish("tool_calls")
	for name, wire := range map[string]string{
		"empty": "", "usage-only": "data: {\"usage\":{\"total_tokens\":1}}\n\n",
		"truncated-text": fixtureText("partial"), "truncated-tool": tool,
		"unfinished-choice":        finished + fixtureChunk(1, map[string]any{"content": "partial"}, ""),
		"malformed-arguments":      fixtureTool("call_1", "lookup", `{`) + fixtureFinish("tool_calls"),
		"malformed-arguments-done": fixtureTool("call_1", "lookup", `{`) + fixtureFinish("tool_calls") + "data: [DONE]\n\n",
		"missing-id":               fixtureTool("", "lookup", `{}`) + fixtureFinish("tool_calls"),
		"missing-name":             fixtureTool("call_1", "", `{}`) + fixtureFinish("tool_calls"),
		"missing-call":             fixtureText("x") + fixtureFinish("tool_calls"),
		"length":                   tool + fixtureFinish("length"), "filter": tool + fixtureFinish("content_filter"),
		"unknown-finish":   tool + fixtureFinish("something-new"),
		"late-error":       finished + "data: {\"error\":{\"message\":\"synthetic failure\"}}\n\n",
		"late-base-error":  finished + "data: {\"base_resp\":{\"status_code\":2013}}\n\n",
		"late-event-error": finished + "event: error\ndata: {}\n\n",
		"late-malformed":   finished + "data: {\n\n",
		"late-output":      finished + fixtureText("unexpected"),
		"changed-finish":   finished + fixtureFinish("stop"),
		"empty-done":       "data: [DONE]\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			stream := fixtureStream(t, context.Background(), "https://api.minimax.cn/v1", "MiniMax-M3", strings.NewReader(wire))
			assertFailedWithoutTools(t, stream)
		})
	}
}

func assertFailedWithoutTools(t *testing.T, stream litellm.Stream) {
	t.Helper()
	for i := 0; i < 100; i++ {
		event, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				t.Fatal("failed stream became ordinary EOF")
			}
			if event, again := stream.Next(); again == nil || event != nil {
				t.Fatal("failure was washed into a subsequent success event")
			}
			return
		}
		switch event.(type) {
		case litellm.ToolUseDone, litellm.DoneEvent:
			t.Fatal("failed stream could execute a tool or signal successful completion")
		}
	}
	t.Fatal("failed stream did not terminate")
}

type eofFixtureReader struct {
	io.Reader
	atEOF func() error
}

func (r eofFixtureReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		return n, r.atEOF()
	}
	return n, err
}

func TestStreamFinishDoesNotHideTransportFailureOrCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wire := fixtureTool("call_1", "lookup", `{}`) + fixtureFinish("tool_calls")
			reader := eofFixtureReader{Reader: strings.NewReader(wire), atEOF: func() error {
				if canceled {
					cancel()
					return io.EOF
				}
				return io.ErrUnexpectedEOF
			}}
			assertFailedWithoutTools(t, fixtureStream(t, ctx, "https://api.minimax.cn/v1", "MiniMax-M3", reader))
		})
	}
}

func TestOtherCompatProvidersStillRequireSentinel(t *testing.T) {
	provider, err := compat.New(compat.Config{BaseURL: "https://example.invalid/v1", HTTPClient: fixtureClient(strings.NewReader(fixtureText("ok") + fixtureFinish("stop")))}, compat.Spec{Name: "unchanged"})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := provider.Stream(context.Background(), &litellm.Request{Model: "fixture", Messages: []litellm.Message{litellm.UserText("fixture")}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := litellm.Collect(stream); err == nil || !strings.Contains(err.Error(), "[DONE]") {
		t.Fatalf("other provider sentinel contract weakened: %v", err)
	}
}

type cancelOnReadFixture struct {
	io.Reader
	cancel context.CancelFunc
}

func (r cancelOnReadFixture) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.cancel()
	}
	return n, err
}

func TestStreamCancellationWhileReadingDoneCannotReleaseTool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opening := strings.NewReader(fixtureTool("call_1", "lookup", "{}"))
	ending := cancelOnReadFixture{Reader: strings.NewReader(fixtureFinish("tool_calls") + "data: [DONE]\n\n"), cancel: cancel}
	stream := fixtureStream(t, ctx, "https://api.minimax.cn/v1", "MiniMax-M3", io.MultiReader(opening, ending))
	assertFailedWithoutTools(t, stream)
}
