package trpcagent

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"
)

// SDK v1.11.2 adds usage across chunks and suppresses empty-choice chunks.
// Capture the unchanged HTTP/SSE bytes instead: usage is a cumulative report,
// not a delta. No prompts/output are retained after each bounded SSE event.
const maxUsageEventBytes = 64 * 1024

type usageCapture struct {
	mu                  sync.Mutex
	calls               int
	done, invalid, seen bool
	finished            bool
	responseID          string
	value               Usage
}

func (c *usageCapture) start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls != 1 {
		c.invalid = true
	}
}
func (c *usageCapture) reject() { c.mu.Lock(); defer c.mu.Unlock(); c.invalid = true }
func (c *usageCapture) event(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		c.invalid = true
		return
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		c.done = true
		return
	}
	var envelope struct {
		Usage   json.RawMessage `json:"usage"`
		ID      string          `json:"id"`
		Choices []struct {
			Index        int     `json:"index"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		c.invalid = true
		return
	}

	if len(envelope.ID) > 256 || (envelope.ID != "" && c.responseID != "" && envelope.ID != c.responseID) {
		c.invalid = true
		return
	}
	if envelope.ID != "" {
		c.responseID = envelope.ID
	}
	for _, choice := range envelope.Choices {
		if choice.Index != 0 {
			c.invalid = true
			return
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			switch *choice.FinishReason {
			case "stop", "length", "content_filter", "tool_calls", "function_call":
				c.finished = true
			default:
				c.invalid = true
				return
			}
		}
	}
	if len(envelope.Usage) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Usage), []byte("null")) {
		return
	}
	if !c.finished {
		c.invalid = true
		return
	}
	var report struct {
		Input  *int64 `json:"prompt_tokens"`
		Output *int64 `json:"completion_tokens"`
		Total  *int64 `json:"total_tokens"`
	}
	if json.Unmarshal(envelope.Usage, &report) != nil || report.Input == nil || report.Output == nil || report.Total == nil {
		c.invalid = true
		return
	}
	const max int64 = 9007199254740991
	if *report.Input < 0 || *report.Output < 0 || *report.Total < 0 || *report.Input > max || *report.Output > max || *report.Total > max || *report.Input+*report.Output != *report.Total || int64(int(*report.Total)) != *report.Total {
		c.invalid = true
		return
	}
	value := Usage{InputTokens: int(*report.Input), OutputTokens: int(*report.Output), TotalTokens: int(*report.Total)}
	if c.seen && c.value != value {
		c.invalid = true
		return
	}
	c.seen = true
	c.value = value
}
func (c *usageCapture) result() (Usage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls != 1 || !c.done || !c.seen || c.invalid {
		return Usage{}, false
	}
	return c.value, true
}

type usageBody struct {
	io.ReadCloser
	capture     *usageCapture
	line, event []byte
	discarded   bool
}

func (b *usageBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	for _, v := range p[:n] {
		if v == '\n' {
			b.lineDone()
			continue
		}
		if b.discarded {
			continue
		}
		if len(b.line) >= maxUsageEventBytes {
			b.capture.reject()
			b.discarded = true
			b.line = nil
			b.event = nil
			continue
		}
		b.line = append(b.line, v)
	}
	if err == io.EOF {
		if len(b.line) > 0 {
			b.lineDone()
		}
		if len(b.event) > 0 {
			b.capture.event(b.event)
			b.event = nil
		}
	} else if err != nil {
		b.capture.reject()
	}
	return n, err
}
func (b *usageBody) lineDone() {
	if b.discarded {
		return
	}
	line := bytes.TrimSuffix(b.line, []byte{'\r'})
	if len(line) == 0 {
		if len(b.event) > 0 {
			b.capture.event(b.event)
			b.event = nil
		}
	} else if bytes.HasPrefix(line, []byte("data:")) {
		data := line[5:]
		if len(data) > 0 && data[0] == ' ' {
			data = data[1:]
		}
		if len(b.event)+len(data)+1 > maxUsageEventBytes {
			b.capture.reject()
			b.discarded = true
			b.event = nil
		} else {
			if len(b.event) > 0 {
				b.event = append(b.event, '\n')
			}
			b.event = append(b.event, data...)
		}
	}
	b.line = b.line[:0]
}
