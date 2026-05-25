package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Seagull2ker/nanobot-go/internal/bus"
	"github.com/Seagull2ker/nanobot-go/internal/types"
)

func pickPort() int {
	return 19300 + int(time.Now().UnixNano()%1000)
}

func TestWebSocketChannelRoundTrip(t *testing.T) {
	port := pickPort()
	messageBus := bus.NewMessageBus()
	ws := NewWebSocketChannel("127.0.0.1", port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ws.Start(ctx, messageBus); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ws.Stop(ctx)

	time.Sleep(200 * time.Millisecond)

	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	t.Logf("connecting to %s", url)

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send a message from the client.
	clientMsg := types.InboundMessage{
		Channel:  "websocket",
		SenderID: "test-user",
		Content:  "hello from test",
	}
	data, _ := json.Marshal(clientMsg)
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Verify the message reaches the bus.
	select {
	case msg := <-messageBus.ConsumeInbound():
		if msg.Content != "hello from test" {
			t.Errorf("content: got %s, want 'hello from test'", msg.Content)
		}
		if msg.Channel != "websocket" {
			t.Errorf("channel: got %s, want websocket", msg.Channel)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for message on bus")
	}

	// Send an outbound reply.
	reply := &types.OutboundMessage{
		Channel: "websocket",
		Content: "response from server",
	}
	messageBus.PublishOutbound(ctx, reply)

	// Verify the client receives the reply.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, replyData, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}

	var replyMsg types.OutboundMessage
	if err := json.Unmarshal(replyData, &replyMsg); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if replyMsg.Content != "response from server" {
		t.Errorf("reply content: got %s", replyMsg.Content)
	}
}

func TestWebSocketChannelMultipleClients(t *testing.T) {
	messageBus := bus.NewMessageBus()
	port := pickPort()
	ws := NewWebSocketChannel("127.0.0.1", port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ws.Start(ctx, messageBus); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ws.Stop(ctx)

	time.Sleep(200 * time.Millisecond)
	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)

	// Connect two clients.
	conn1, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	defer conn1.Close()

	conn2, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer conn2.Close()

	// Send message from client 1.
	msg1, _ := json.Marshal(types.InboundMessage{Channel: "websocket", Content: "msg1"})
	conn1.WriteMessage(websocket.TextMessage, msg1)

	select {
	case m := <-messageBus.ConsumeInbound():
		if m.Content != "msg1" {
			t.Errorf("got %s", m.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for msg1")
	}

	// Send message from client 2.
	msg2, _ := json.Marshal(types.InboundMessage{Channel: "websocket", Content: "msg2"})
	conn2.WriteMessage(websocket.TextMessage, msg2)

	select {
	case m := <-messageBus.ConsumeInbound():
		if m.Content != "msg2" {
			t.Errorf("got %s", m.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for msg2")
	}
}

func TestWebSocketChannelStopClosesConnections(t *testing.T) {
	messageBus := bus.NewMessageBus()
	port := pickPort()
	ws := NewWebSocketChannel("127.0.0.1", port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ws.Start(ctx, messageBus); err != nil {
		t.Fatalf("start: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Stop the channel.
	ws.Stop(ctx)

	// Connection should close.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Error("expected connection to close after channel stop")
	}
}

func TestSendNonWebSocketChannelMessage(t *testing.T) {
	messageBus := bus.NewMessageBus()
	port := pickPort()
	ws := NewWebSocketChannel("127.0.0.1", port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ws.Start(ctx, messageBus)
	defer ws.Stop(ctx)

	// Send a message targeting a different channel — should be silently ignored.
	err := ws.Send(ctx, &types.OutboundMessage{
		Channel: "feishu",
		Content: "should be ignored",
	})
	if err != nil {
		t.Errorf("Send should return nil for non-websocket messages: %v", err)
	}
}
