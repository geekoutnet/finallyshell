package ai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

func errorMessage(data []byte) string {
	var result struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(data, &result) != nil {
		return ""
	}
	var object struct{ Message string }
	var text string
	if json.Unmarshal(result.Error, &object) == nil && object.Message != "" {
		return object.Message
	}
	if json.Unmarshal(result.Error, &text) == nil && text != "" {
		return text
	}
	return result.Message
}

// readStream reads a chat completion's events, its text to delta and the
// tools it calls to calls, nil for a chat that offers none.
func readStream(source io.Reader, delta func(string), calls *[]ToolCall) error {
	scanner := bufio.NewScanner(source)
	const maxEvent = 1 << 20
	scanner.Buffer(make([]byte, 4096), maxEvent)
	scanner.Split(scanEventLine)
	var data strings.Builder
	var event string
	var finished bool
	readEvent := func() (bool, error) {
		payload := strings.TrimSpace(data.String())
		kind := event
		data.Reset()
		event = ""
		if kind == "error" {
			if reason := errorMessage([]byte(payload)); reason != "" {
				return false, errors.New(reason)
			}
			return false, errors.New("provider error: " + payload)
		}
		if payload == "" || payload == "{}" {
			return false, nil
		}
		if payload == "[DONE]" {
			return true, nil
		}
		if reason := errorMessage([]byte(payload)); reason != "" {
			return false, errors.New(reason)
		}
		var chunk struct {
			Choices []struct {
				Index int
				Delta struct {
					Content   string
					ToolCalls []toolCallDelta `json:"tool_calls"`
				}
				FinishReason string `json:"finish_reason"`
			}
			Error json.RawMessage
		}
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			return false, errors.New("invalid chat completion event")
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return false, errors.New("provider returned a chat error")
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.Delta.Content != "" {
				delta(choice.Delta.Content)
			}
			if calls != nil {
				for _, part := range choice.Delta.ToolCalls {
					if err := part.addTo(calls); err != nil {
						return false, err
					}
				}
			}
			finished = finished || choice.FinishReason != ""
		}
		return false, nil
	}
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
		}
		if line == "" {
			if done, err := readEvent(); done || err != nil {
				return err
			}
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "data":
			if data.Len()+len(value) > maxEvent {
				return errors.New("chat completion event too large")
			}
			data.WriteString(value)
			data.WriteByte('\n')
		case "event":
			event = value
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if done, err := readEvent(); done || err != nil {
		return err
	}
	if !finished {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func scanEventLine(data []byte, atEOF bool) (int, []byte, error) {
	if index := bytes.IndexAny(data, "\r\n"); index >= 0 {
		end := index + 1
		if data[index] == '\r' {
			if end == len(data) && !atEOF {
				return 0, nil, nil
			}
			if end < len(data) && data[end] == '\n' {
				end++
			}
		}
		return end, data[:index], nil
	}
	if atEOF && len(data) != 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// toolCallDelta is a piece of a tool call, streamed: the first names the
// call and the function, the others add to its arguments.
type toolCallDelta struct {
	Index    *int
	ID       string
	Type     string
	Function struct{ Name, Arguments string }
}

// maxToolCalls and maxToolArguments bound what a reply may call.
const (
	maxToolCalls     = 16
	maxToolArguments = 64 << 10
)

// addTo adds the piece to the calls it belongs to: by its index, or, as
// some providers stream whole calls without one, the last call unless it
// names a new one.
func (part toolCallDelta) addTo(calls *[]ToolCall) error {
	list := *calls
	at := len(list) - 1
	switch {
	case part.Index != nil:
		at = *part.Index
	case part.ID != "" && (at < 0 || list[at].ID != part.ID):
		at = len(list)
	}
	if at < 0 || at > len(list) || at >= maxToolCalls {
		return errors.New("invalid tool call")
	}
	if at == len(list) {
		list = append(list, ToolCall{Type: "function"})
	}
	call := &list[at]
	if part.ID != "" {
		call.ID = part.ID
	}
	call.Function.Name += part.Function.Name
	call.Function.Arguments += part.Function.Arguments
	if len(call.Function.Name) > 256 || len(call.Function.Arguments) > maxToolArguments {
		return errors.New("tool call too large")
	}
	*calls = list
	return nil
}
