package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
)

func TestDesktopAISSE(t *testing.T) {
	for _, sample := range []struct{ input, text, failure string }{
		{": heartbeat\r\ndata: {\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\r\n\r\ndata: [DONE]\n\ndata: broken\n\n", "你好", ""},
		{"data: {\n data: ignored\ndata: \"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]", "ok", ""},
		{"data: {\"choices\":[{\"delta\":{\"content\":\"CR\"}}]}\r\rdata: [DONE]\r\r", "CR", ""},
		{"event: error\ndata: {\"error\":{\"message\":\"quota exceeded\"}}\n\n", "", "quota exceeded"},
		{"data: {\"error\":{\"message\":\"denied\"}}\n\n", "", "denied"},
		{"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", "partial", "unexpected EOF"},
	} {
		var output strings.Builder
		err := readStream(iotest.OneByteReader(strings.NewReader(sample.input)), func(text string) { output.WriteString(text) })
		if output.String() != sample.text || sample.failure == "" && err != nil || sample.failure != "" && (err == nil || !strings.Contains(err.Error(), sample.failure)) {
			t.Errorf("got %q, %v; want %q, %q", output.String(), err, sample.text, sample.failure)
		}
	}
}

func TestDesktopAIPresets(t *testing.T) {
	for _, preset := range Presets() {
		if preset.ID == "custom" {
			continue
		}
		t.Run(preset.ID, func(t *testing.T) {
			client := Client{Endpoint: preset.Endpoint, Key: "test-key", Model: "model"}
			request, err := client.request(context.Background(), http.MethodGet, "models", nil)
			if err != nil || request.URL.String() != preset.Endpoint+"/models" || request.Header.Get("Authorization") != "Bearer test-key" {
				t.Fatalf("request = %v, %v", request, err)
			}
		})
	}
	azure := Client{Endpoint: "https://example.openai.azure.com/openai/deployments/example/?api-version=2024-10-21", Key: "test-key", Model: "example"}
	request, err := azure.request(context.Background(), http.MethodPost, "chat/completions", nil)
	if err != nil || request.URL.String() != "https://example.openai.azure.com/openai/deployments/example/chat/completions?api-version=2024-10-21" || request.Header.Get("api-key") != "test-key" {
		t.Fatalf("azure request = %v, %v", request, err)
	}
	for _, endpoint := range []string{"", "http://example.com/v1", "https://user@invalid.example/v1", "https://example.com/v1?api_key=fake"} {
		if _, err := BaseURL(endpoint); err == nil {
			t.Errorf("accepted %q", endpoint)
		}
	}
}

func TestDesktopAIClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			io.WriteString(w, `{"data":[{"id":"z"},{"id":"a"},{"id":"z"}]}`)
		case "/v1/chat/completions":
			var body completion
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil || !body.Stream || body.Model != "model" || len(body.Messages) != 1 {
				t.Errorf("unexpected request: %v %+v", r.Method, body)
				return
			}
			switch body.Messages[0].Content {
			case "wait":
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			case "error":
				w.WriteHeader(http.StatusUnauthorized)
				io.WriteString(w, `{"error":{"message":"Rejected artificial-key"}}`)
			case "redirect":
				http.Redirect(w, r, "/v1/leak", http.StatusTemporaryRedirect)
			default:
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n")
			}
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := Client{Endpoint: server.URL + "/v1", Model: "model", Key: "artificial-key"}
	models, err := client.Models(context.Background())
	if err != nil || strings.Join(models, ",") != "a,z" {
		t.Fatalf("models = %v, %v", models, err)
	}
	var output string
	err = client.Stream(context.Background(), []Message{{Role: "user", Content: "hello"}}, func(text string) { output += text })
	if err != nil || output != "hi" {
		t.Fatalf("stream = %q, %v", output, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = client.Stream(ctx, []Message{{Role: "user", Content: "wait"}}, func(string) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
	for text, expected := range map[string]string{"error": "HTTP 401: Rejected [redacted]", "redirect": "HTTP 307"} {
		err = client.Stream(context.Background(), []Message{{Role: "user", Content: text}}, func(string) {})
		if err == nil || !strings.Contains(err.Error(), expected) || strings.Contains(err.Error(), client.Key) {
			t.Errorf("%s = %v", text, err)
		}
	}
}

func TestDesktopAIPrivacyAndCommand(t *testing.T) {
	input := "pass" + "word=hunter2\nAuthorization: Bearer fake-auth\n\"api_key\": \"my-special-key\"\ncmd --passphrase 'foo bar'\nhttps://bob:private-phrase@example.net\n-----BEGIN OPENSSH " + "PRIVATE KEY-----\nkey-body-fixture\n-----END OPENSSH " + "PRIVATE KEY-----\nkeep this"
	masked := Mask(input)
	for _, secret := range []string{"hunter2", "fake-auth", "my-special-key", "foo bar", "private-phrase", "key-body-fixture"} {
		if strings.Contains(masked, secret) {
			t.Errorf("leaked %q: %s", secret, masked)
		}
	}
	if !strings.Contains(masked, "keep this") {
		t.Fatal(masked)
	}
	for _, sample := range []struct{ reply, want string }{
		{"Try this:\n```bash\nls -la\n```", "ls -la"}, {"`df -h`", "df -h"}, {"echo hi\nwhoami", ""}, {"echo hi\x1b", ""}, {"```python\nprint(1)\n```", ""}, {"````sh\nls\n````", "ls"},
	} {
		if got := Command(sample.reply); got != sample.want {
			t.Errorf("Command(%q) = %q, want %q", sample.reply, got, sample.want)
		}
	}
	for _, command := range []string{"sudo rm -rf /", "sudo rm -r -f -- '/'", "echo ok; mkfs.ext4 /dev/vdb", "dd if=image of=/dev/vda"} {
		if !HighRisk(command) {
			t.Errorf("not flagged: %q", command)
		}
	}
}
