package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/Seagull2ker/nanobot-go/internal/bus"
	"github.com/Seagull2ker/nanobot-go/internal/types"
)

// WebSocketChannel serves WebSocket connections for WebUI communication.
type WebSocketChannel struct {
	mu       sync.RWMutex
	conns    map[string]*websocket.Conn
	bus      *bus.MessageBus
	host     string
	port     int
	server   *http.Server
	upgrader websocket.Upgrader
}

// NewWebSocketChannel creates a WebSocket channel for WebUI frontend communication.
func NewWebSocketChannel(host string, port int) *WebSocketChannel {
	return &WebSocketChannel{
		conns: make(map[string]*websocket.Conn),
		host:  host,
		port:  port,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (w *WebSocketChannel) Name() string           { return "websocket" }
func (w *WebSocketChannel) SupportsStreaming() bool { return true }

func (w *WebSocketChannel) Start(ctx context.Context, bus *bus.MessageBus) error {
	w.bus = bus
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", w.handleWS)

	addr := fmt.Sprintf("%s:%d", w.host, w.port)
	w.server = &http.Server{Addr: addr, Handler: mux}

	slog.Info("websocket channel starting", "addr", addr)
	go func() {
		if err := w.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("websocket server error", "error", err)
		}
	}()

	go w.consumeOutbound(ctx)
	return nil
}

func (w *WebSocketChannel) Stop(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, conn := range w.conns {
		conn.Close()
		delete(w.conns, id)
	}
	if w.server != nil {
		return w.server.Shutdown(ctx)
	}
	return nil
}

func (w *WebSocketChannel) Send(ctx context.Context, msg *types.OutboundMessage) error {
	if msg.Channel != "websocket" {
		return nil
	}

	data, _ := json.Marshal(msg)

	w.mu.RLock()
	defer w.mu.RUnlock()

	for id, conn := range w.conns {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			slog.Warn("websocket write error", "conn", id, "error", err)
		}
	}
	return nil
}

func (w *WebSocketChannel) handleWS(rw http.ResponseWriter, r *http.Request) {
	conn, err := w.upgrader.Upgrade(rw, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err)
		return
	}

	connID := r.RemoteAddr
	w.mu.Lock()
	w.conns[connID] = conn
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		delete(w.conns, connID)
		w.mu.Unlock()
		conn.Close()
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var msg types.InboundMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		msg.Channel = "websocket"
		w.bus.PublishInbound(r.Context(), &msg)
	}
}

func (w *WebSocketChannel) consumeOutbound(ctx context.Context) {
	ch := w.bus.ConsumeOutbound()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			w.Send(ctx, msg)
		}
	}
}
