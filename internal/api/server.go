package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Seagull2ker/nanobot-go/internal/types"
)

// MessageHandler handles chat completion requests.
type MessageHandler func(ctx context.Context, msg *types.InboundMessage) (*types.OutboundMessage, error)

// Server provides an OpenAI-compatible HTTP API.
type Server struct {
	host    string
	port    int
	timeout time.Duration
	server  *http.Server
	handler MessageHandler
}

// NewServer creates an API server.
func NewServer(host string, port int, timeout time.Duration) *Server {
	return &Server{
		host:    host,
		port:    port,
		timeout: timeout,
	}
}

// OnMessage sets the chat completion handler.
func (s *Server) OnMessage(h MessageHandler) {
	s.handler = h
}

// Start begins listening.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletion)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/health", s.handleHealth)

	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	s.server = &http.Server{Addr: addr, Handler: mux}

	slog.Info("api server starting", "addr", addr)
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("api server error", "error", err)
		}
	}()
	return nil
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

func (s *Server) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Stream bool `json:"stream"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if len(req.Messages) == 0 {
		http.Error(w, "Messages required", http.StatusBadRequest)
		return
	}

	lastMsg := req.Messages[len(req.Messages)-1]
	inbound := &types.InboundMessage{
		Channel:  "api",
		ChatID:   r.RemoteAddr,
		Content:  lastMsg.Content,
		Metadata: map[string]any{"model": req.Model},
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	resp, err := s.handler(ctx, inbound)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":     fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object": "chat.completion",
		"model":  req.Model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": resp.Content,
				},
				"finish_reason": "stop",
			},
		},
	})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data": []map[string]any{
			{"id": "nanobot", "object": "model", "owned_by": "nanobot"},
		},
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
