package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"time"
)

func main() {
	listen := flag.String("listen", ":8342", "mock upstream listen address")
	timeoutDelay := flag.Duration("timeout-delay", 10*time.Second, "mock timeout response delay")
	flag.Parse()
	if err := http.ListenAndServe(*listen, newMockUpstreamHandler(*timeoutDelay)); err != nil {
		panic(err)
	}
}

func newMockUpstreamHandler(timeoutDelay time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, `{"error":{"type":"bad_request"}}`, http.StatusBadRequest)
			return
		}
		switch request.Model {
		case "mock-success":
			writeChatCompletion(w, request.Model)
		case "mock-no-usage":
			writeChatCompletionWithoutUsage(w, request.Model)
		case "mock-stream":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "data: {\"id\":\"mock-stream\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"streamed\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"id\":\"mock-stream\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":4}}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		case "mock-429":
			writeError(w, http.StatusTooManyRequests, "rate_limit")
		case "mock-500":
			writeError(w, http.StatusInternalServerError, "server_error")
		case "mock-timeout":
			select {
			case <-time.After(timeoutDelay):
				writeError(w, http.StatusGatewayTimeout, "mock_timeout")
			case <-r.Context().Done():
			}
		default:
			writeError(w, http.StatusNotFound, "unknown_model")
		}
	})
}

func writeChatCompletionWithoutUsage(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "mock-no-usage", "object": "chat.completion", "model": model,
		"choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": "mock success without usage"}, "finish_reason": "stop"}},
	})
}

func writeChatCompletion(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "mock-success", "object": "chat.completion", "model": model,
		"choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": "mock success"}, "finish_reason": "stop"}},
		"usage":   map[string]int{"prompt_tokens": 11, "completion_tokens": 3},
	})
}

func writeError(w http.ResponseWriter, status int, kind string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"type": kind, "message": kind}})
}
