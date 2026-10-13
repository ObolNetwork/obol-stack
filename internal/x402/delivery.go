package x402

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Settle-on-delivery (issue #821): on chat-shaped routes the seller gateway
// settles only when the upstream demonstrably delivered an answer. An agent
// whose LLM provider failed can still reply 200 with an empty completion;
// settling on the status alone charged the buyer for nothing.

// deliveryBufferCap bounds how much of a non-SSE response the interceptor
// holds back while it waits to judge the body. Chat completions are a few KiB;
// a response that outgrows the cap has plainly delivered content, so the
// interceptor falls back to settling at that point (status still uncommitted,
// so a settle failure can still become a 503) and streams the rest through.
const deliveryBufferCap = 4 << 20

// sseEventCap bounds a single SSE event the value tap assembles. A longer
// event is treated as delivered value (fail open): parsing it would mean
// buffering unbounded data, and an oversized event is not the empty-reply
// failure shape this check exists for.
const sseEventCap = 1 << 20

// deliveryVerdict is the outcome of inspecting a response for value.
type deliveryVerdict int

const (
	// deliveryUnknown: the shape is not one we can judge (not JSON, not
	// chat-completions shaped, compressed with an unsupported coding).
	// Callers fail open and settle on any non-empty body.
	deliveryUnknown deliveryVerdict = iota
	// deliveryValue: at least one choice carried content or tool calls.
	deliveryValue
	// deliveryNoValue: chat-shaped but empty (no content, no tool calls),
	// or a JSON error object with no choices.
	deliveryNoValue
)

// judgeChatJSON inspects one chat-completions JSON object — a full
// response (choiceKey "message") or a stream chunk (choiceKey "delta").
func judgeChatJSON(raw []byte, choiceKey string) deliveryVerdict {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return deliveryUnknown
	}
	rawChoices, hasChoices := doc["choices"]
	if !hasChoices {
		if isJSONPresent(doc["error"]) {
			return deliveryNoValue
		}
		return deliveryUnknown
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(rawChoices, &choices); err != nil {
		return deliveryUnknown
	}
	for _, choice := range choices {
		// Legacy /v1/completions carries the text directly on the choice.
		if nonEmptyJSONString(choice["text"]) {
			return deliveryValue
		}
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(choice[choiceKey], &msg); err != nil {
			continue
		}
		if messageHasValue(msg) {
			return deliveryValue
		}
	}
	return deliveryNoValue
}

// messageHasValue reports whether an assistant message (or stream delta)
// carries anything the buyer paid for: text content, reasoning text,
// tool/function calls, a refusal, or audio. A role-only message is not
// value. Reasoning counts: otherwise a buyer could cap max_tokens below the
// answer and receive a reasoning model's thinking unpaid.
func messageHasValue(msg map[string]json.RawMessage) bool {
	if nonEmptyJSONString(msg["content"]) || nonEmptyJSONArray(msg["content"]) {
		return true
	}
	if nonEmptyJSONString(msg["reasoning_content"]) || nonEmptyJSONString(msg["reasoning"]) {
		return true
	}
	if nonEmptyJSONArray(msg["tool_calls"]) {
		return true
	}
	if nonEmptyJSONString(msg["refusal"]) {
		return true
	}
	return isJSONObject(msg["function_call"]) || isJSONObject(msg["audio"])
}

func nonEmptyJSONString(raw json.RawMessage) bool {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return false
	}
	return strings.TrimSpace(s) != ""
}

func nonEmptyJSONArray(raw json.RawMessage) bool {
	var a []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &a) != nil {
		return false
	}
	return len(a) > 0
}

func isJSONObject(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '{'
}

func isJSONPresent(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && !bytes.Equal(t, []byte("null"))
}

// judgeBufferedBody decides whether a complete non-SSE body delivered value.
// A zero-byte body is no value — checked before any parse, because a parse
// error must fail open and would otherwise settle the exact empty-reply case.
func judgeBufferedBody(body []byte, header http.Header) deliveryVerdict {
	if len(body) == 0 {
		return deliveryNoValue
	}
	decoded, ok := decodeForInspection(body, header.Get("Content-Encoding"))
	if !ok {
		return deliveryUnknown
	}
	if len(bytes.TrimSpace(decoded)) == 0 {
		return deliveryNoValue
	}
	return judgeChatJSON(decoded, "message")
}

// decodeForInspection undoes a gzip Content-Encoding (bounded) so the body
// can be judged. Other codings cannot be judged; ok=false fails open.
func decodeForInspection(body []byte, contentEncoding string) ([]byte, bool) {
	switch strings.ToLower(strings.TrimSpace(contentEncoding)) {
	case "", "identity":
		return body, true
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, false
		}
		defer zr.Close()
		out, err := io.ReadAll(io.LimitReader(zr, deliveryBufferCap+1))
		if err != nil || len(out) > deliveryBufferCap {
			return nil, false
		}
		return out, true
	default:
		return nil, false
	}
}

// sseValueTap watches an SSE byte stream as it is written to the client —
// it never holds bytes back — and records whether any chat-completions
// chunk carried content or tool-call deltas. Events are assembled per the
// SSE framing (data: lines joined until a blank line).
type sseValueTap struct {
	line      []byte
	data      []byte
	hasData   bool
	chatShape bool // saw at least one judgeable chunk (choices or error)
	value     bool // saw a delta with content / tool calls
}

// feed consumes bytes already delivered to the client.
func (t *sseValueTap) feed(b []byte) {
	for len(b) > 0 && !t.value {
		nl := bytes.IndexByte(b, '\n')
		if nl < 0 {
			t.appendLine(b)
			return
		}
		t.appendLine(b[:nl])
		b = b[nl+1:]
		t.processLine()
	}
}

// close dispatches any trailing, unterminated event at end of stream.
func (t *sseValueTap) close() {
	if t.value {
		return
	}
	if len(t.line) > 0 {
		t.processLine()
	}
	t.dispatch()
}

// delivered reports whether the stream should be treated as having
// delivered value. A stream that never produced a judgeable chat chunk is
// not chat-shaped, so the tap fails open.
func (t *sseValueTap) delivered() bool {
	return t.value || !t.chatShape
}

func (t *sseValueTap) appendLine(b []byte) {
	if len(t.line)+len(b) > sseEventCap {
		t.value = true // oversized event: fail open, stop inspecting
		t.line, t.data = nil, nil
		return
	}
	t.line = append(t.line, b...)
}

func (t *sseValueTap) processLine() {
	line := bytes.TrimSuffix(t.line, []byte("\r"))
	t.line = t.line[:0]
	if len(line) == 0 {
		t.dispatch()
		return
	}
	if !bytes.HasPrefix(line, []byte("data:")) {
		return // event:, id:, retry:, comments — no payload
	}
	payload := bytes.TrimPrefix(line[len("data:"):], []byte(" "))
	if len(t.data)+len(payload)+1 > sseEventCap {
		t.value = true
		t.data = nil
		return
	}
	if t.hasData {
		t.data = append(t.data, '\n')
	}
	t.data = append(t.data, payload...)
	t.hasData = true
}

func (t *sseValueTap) dispatch() {
	if !t.hasData {
		return
	}
	data := bytes.TrimSpace(t.data)
	t.data = t.data[:0]
	t.hasData = false
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return
	}
	switch judgeChatJSON(data, "delta") {
	case deliveryValue:
		t.chatShape = true
		t.value = true
	case deliveryNoValue:
		t.chatShape = true
	case deliveryUnknown:
		// Not a chat chunk (custom agent event, non-JSON): no evidence.
	}
}
