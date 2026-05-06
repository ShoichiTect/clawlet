package tui

import (
	"strings"
	"testing"
)

func TestConsumeSSE_ParsesEvents(t *testing.T) {
	input := strings.NewReader("event: tool_start\n" +
		"data: {\"name\":\"exec\",\"args\":\"{}\"}\n\n" +
		"event: done\n" +
		"data: {\"type\":\"done\"}\n\n")

	var events []ChatStreamEvent
	if err := consumeSSE(input, func(ev ChatStreamEvent) {
		events = append(events, ev)
	}); err != nil {
		t.Fatalf("consumeSSE error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != "tool_start" || events[0].Name != "exec" || events[0].Args != "{}" {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].Type != "done" {
		t.Fatalf("unexpected second event: %+v", events[1])
	}
}

func TestGatewayErrorMessage_PrefersJSONError(t *testing.T) {
	got := gatewayErrorMessage([]byte(`{"error":"llm api error"}`), "500 Internal Server Error")
	if got != "llm api error" {
		t.Fatalf("expected JSON error, got %q", got)
	}
}
