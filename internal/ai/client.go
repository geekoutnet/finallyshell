package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/netproxy"
)

type Preset struct{ ID, Label, Endpoint string }

func Presets() []Preset {
	return []Preset{
		{"openai", "OpenAI", "https://api.openai.com/v1"},
		{"deepseek", "DeepSeek", "https://api.deepseek.com/v1"},
		{"qwen", "Qwen / DashScope", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{"moonshot", "Moonshot / Kimi", "https://api.moonshot.cn/v1"},
		{"zhipu", "Zhipu GLM", "https://open.bigmodel.cn/api/paas/v4"},
		{"ollama", "Ollama", "http://localhost:11434/v1"},
		{"openrouter", "OpenRouter", "https://openrouter.ai/api/v1"},
		{"custom", "", ""},
	}
}

var ErrEndpoint = errors.New("expected an HTTPS base URL or a local HTTP base URL, without credentials")

func BaseURL(endpoint string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" ||
		parsed.Scheme != "https" && !(parsed.Scheme == "http" && Loopback(parsed)) {
		return nil, ErrEndpoint
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return nil, ErrEndpoint
	}
	for name := range query {
		switch strings.ToLower(name) {
		case "key", "api_key", "api-key", "apikey", "access_token", "access-token", "token", "pass" + "word", "secret", "sig", "signature", "authorization":
			return nil, ErrEndpoint
		}
	}
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	parsed.RawQuery = query.Encode()
	parsed.ForceQuery = false
	return parsed, nil
}

func Loopback(endpoint *url.URL) bool {
	host := endpoint.Hostname()
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}

// Message is a message of a chat: an assistant's may call tools, and a
// "tool" message answers the call ToolCallID names.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is the model asking to call a function, its arguments JSON.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool is a function the model may call, its parameters a JSON schema.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type completion struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	Stream   bool      `json:"stream"`
}

type Client struct{ Endpoint, Key, Model string }

var transport = func() *http.Transport {
	transport := netproxy.Transport().Clone()
	transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 30 * time.Second
	return transport
}()

var httpClient = &http.Client{
	Transport: transport,
	Timeout:   3 * time.Minute,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func (client Client) request(ctx context.Context, method, route string, body io.Reader) (*http.Request, error) {
	base, err := BaseURL(client.Endpoint)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, base.JoinPath(route).String(), body)
	if err != nil {
		return nil, ErrEndpoint
	}
	if client.Key != "" {
		if strings.HasSuffix(base.Hostname(), ".openai.azure.com") && strings.Contains(base.Path, "/deployments/") {
			request.Header.Set("api-key", client.Key)
		} else {
			request.Header.Set("Authorization", "Bearer "+client.Key)
		}
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "text/event-stream")
	}
	return request, nil
}

func (client Client) do(request *http.Request) (*http.Response, error) {
	response, err := httpClient.Do(request)
	if err != nil {
		if request.Context().Err() != nil {
			return nil, request.Context().Err()
		}
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, errors.New(client.SafeText(err.Error()))
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
	message := errorMessage(body)
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if message == "" {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return nil, fmt.Errorf("HTTP %d: %s", response.StatusCode, Limit(client.SafeText(message), 1000))
}

func (client Client) SafeText(text string) string {
	if client.Key != "" {
		text = strings.ReplaceAll(text, client.Key, "[redacted]")
	}
	return Mask(text)
}

func (client Client) Models(ctx context.Context) ([]string, error) {
	request, err := client.request(ctx, http.MethodGet, "models", nil)
	if err != nil {
		return nil, err
	}
	response, err := client.do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var result struct {
		Data []struct{ ID string } `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&result); err != nil {
		return nil, errors.New("invalid model list")
	}
	models := make(map[string]bool)
	for _, model := range result.Data {
		if model.ID != "" && len(model.ID) <= 256 && (client.Key == "" || !strings.Contains(model.ID, client.Key)) {
			models[model.ID] = true
		}
	}
	out := make([]string, 0, len(models))
	for model := range models {
		out = append(out, model)
	}
	sort.Strings(out)
	return out, nil
}

func (client Client) Stream(ctx context.Context, messages []Message, delta func(string)) error {
	_, err := client.StreamTools(ctx, messages, nil, delta)
	return err
}

// StreamTools streams the model's reply to messages, its text to delta,
// offering it tools; it returns the calls the model made of them.
func (client Client) StreamTools(ctx context.Context, messages []Message, tools []Tool, delta func(string)) ([]ToolCall, error) {
	body, err := json.Marshal(completion{client.Model, messages, tools, true})
	if err != nil {
		return nil, err
	}
	request, err := client.request(ctx, http.MethodPost, "chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	response, err := client.do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var calls []ToolCall
	if err := readStream(io.LimitReader(response.Body, 16<<20), delta, &calls); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New(Limit(client.SafeText(err.Error()), 1000))
	}
	return calls, nil
}
