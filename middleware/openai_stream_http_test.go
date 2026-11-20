package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestChatStreamTerminalEventsOverHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, last, wantTerminal string
	}{
		{"error", `{"error":"parser failed"}`, "error:parser failed"},
		{"success", `{"model":"m","done":true,"done_reason":"stop"}`, "finish:stop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.POST("/v1/chat/completions", ChatMiddleware(), func(c *gin.Context) {
				for _, line := range []string{`{"model":"m","message":{"role":"assistant","content":"partial"}}`, tc.last} {
					if _, err := c.Writer.Write([]byte(line + "\n")); err != nil {
						t.Error(err)
						return
					}
					c.Writer.Flush()
				}
			})
			server := httptest.NewServer(router)
			defer server.Close()
			res, err := server.Client().Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`))
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
				t.Fatalf("unexpected response: %s %q: %s", res.Status, res.Header.Get("Content-Type"), body)
			}
			var events []string
			for _, frame := range strings.Split(strings.TrimSpace(string(body)), "\n\n") {
				payload, ok := strings.CutPrefix(frame, "data: ")
				if !ok {
					t.Fatalf("invalid SSE frame %q", frame)
				}
				if payload == "[DONE]" {
					events = append(events, payload)
					continue
				}
				var chunk struct {
					Error   struct{ Message string }
					Choices []struct {
						Delta        struct{ Content string }
						FinishReason string `json:"finish_reason"`
					}
				}
				if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
					t.Fatal(err)
				}
				if chunk.Error.Message != "" {
					events = append(events, "error:"+chunk.Error.Message)
				}
				for _, choice := range chunk.Choices {
					if choice.Delta.Content != "" {
						events = append(events, "content:"+choice.Delta.Content)
					}
					if choice.FinishReason != "" {
						events = append(events, "finish:"+choice.FinishReason)
					}
				}
			}
			want := []string{"content:partial", tc.wantTerminal, "[DONE]"}
			if !slices.Equal(events, want) {
				t.Fatalf("events = %q, want %q", events, want)
			}
		})
	}
}
