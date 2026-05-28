package tools

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Seagull2ker/nanobot-go/internal/config"
	"github.com/cloudwego/eino-ext/components/tool/mcp/officialmcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var logMCP = slog.With("module", "mcp")

type mcpSessionEntry struct {
	serverName string
	session    *mcp.ClientSession
}

var (
	mcpSessionsMu sync.Mutex
	mcpSessions   []mcpSessionEntry
)

// ConnectMCPServer dials an MCP server, registers the session, and returns
// InvokableTools for each tool the server advertises.
func ConnectMCPServer(ctx context.Context, cfg config.MCPServerConfig) ([]tool.InvokableTool, error) {
	transportType := cfg.Type
	if transportType == "" {
		switch {
		case cfg.Command != "":
			transportType = "stdio"
		case cfg.URL != "":
			if strings.HasSuffix(strings.TrimRight(cfg.URL, "/"), "/sse") {
				transportType = "sse"
			} else {
				transportType = "streamableHttp"
			}
		default:
			return nil, fmt.Errorf("MCP server '%s': no command or url configured", cfg.Name)
		}
	}

	timeout := time.Duration(cfg.ToolTimeout) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	var transport mcp.Transport
	switch transportType {
	case "stdio":
		cmd := exec.Command(cfg.Command, cfg.Args...)
		if len(cfg.Env) > 0 {
			env := cmd.Environ()
			for k, v := range cfg.Env {
				env = append(env, fmt.Sprintf("%s=%s", k, v))
			}
			cmd.Env = env
		}
		transport = &mcp.CommandTransport{Command: cmd}

	case "sse":
		t := &mcp.SSEClientTransport{Endpoint: cfg.URL}
		if len(cfg.Headers) > 0 {
			t.HTTPClient = &http.Client{Transport: &headerTransport{base: http.DefaultTransport, headers: cfg.Headers}}
		}
		transport = t

	case "streamableHttp":
		t := &mcp.StreamableClientTransport{Endpoint: cfg.URL}
		if len(cfg.Headers) > 0 {
			t.HTTPClient = &http.Client{Transport: &headerTransport{base: http.DefaultTransport, headers: cfg.Headers}}
		}
		transport = t

	default:
		return nil, fmt.Errorf("MCP server '%s': unknown transport type '%s'", cfg.Name, transportType)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "nanobot", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("MCP server '%s': connect: %w", cfg.Name, err)
	}

	registerMCPSession(cfg.Name, session)

	allowAll := len(cfg.EnabledTools) == 0
	if !allowAll {
		for _, t := range cfg.EnabledTools {
			if t == "*" {
				allowAll = true
				break
			}
		}
	}

	var toolNameList []string
	if !allowAll {
		for _, name := range cfg.EnabledTools {
			prefix := "mcp_" + cfg.Name + "_"
			toolNameList = append(toolNameList, strings.TrimPrefix(name, prefix))
		}
	}

	baseTools, err := officialmcp.GetTools(ctx, &officialmcp.Config{
		Cli:          session,
		ToolNameList: toolNameList,
	})
	if err != nil {
		return nil, fmt.Errorf("MCP server '%s': list tools: %w", cfg.Name, err)
	}

	var tools []tool.InvokableTool
	for _, bt := range baseTools {
		if it, ok := bt.(tool.InvokableTool); ok {
			tools = append(tools, &mcpToolWrapper{inner: it, serverName: cfg.Name, toolTimeout: timeout})
		}
	}

	logMCP.Info("MCP server connected", "server", cfg.Name, "tools", len(tools))
	return tools, nil
}

func registerMCPSession(name string, session *mcp.ClientSession) {
	mcpSessionsMu.Lock()
	defer mcpSessionsMu.Unlock()
	mcpSessions = append(mcpSessions, mcpSessionEntry{serverName: name, session: session})
}

// CloseMCPConnections closes all active MCP sessions.
func CloseMCPConnections() int {
	mcpSessionsMu.Lock()
	sessions := mcpSessions
	mcpSessions = nil
	mcpSessionsMu.Unlock()

	for _, entry := range sessions {
		if entry.session != nil {
			entry.session.Close()
		}
	}
	return len(sessions)
}

type mcpToolWrapper struct {
	inner       tool.InvokableTool
	serverName  string
	toolTimeout time.Duration
}

func (w *mcpToolWrapper) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.inner.Info(ctx)
}

func (w *mcpToolWrapper) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	tCtx, cancel := context.WithTimeout(ctx, w.toolTimeout)
	defer cancel()

	result, err := w.inner.InvokableRun(tCtx, args, opts...)
	if tCtx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("(MCP tool call timed out after %v)", w.toolTimeout), nil
	}
	if err != nil {
		return fmt.Sprintf("(MCP tool call failed: %v)", err), nil
	}
	if result == "" {
		return "(no output)", nil
	}
	return result, nil
}

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}
