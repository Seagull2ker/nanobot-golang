package command

import (
	"context"
	"strings"
)

// Result is the result of a command dispatch.
type Result struct {
	Content string // response text for simple commands
	Handled bool   // true if the command was matched and handled
}

// AgentOps is the interface the agent provides for stateful commands.
type AgentOps interface {
	HandleNewSession(ctx context.Context, sessionID string) (string, error)
	HandleStop(ctx context.Context, sessionID string) (string, error)
	GetToolInfos() []ToolInfo
}

// ToolInfo describes a single tool for the /tools command.
type ToolInfo struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON Schema
}

// Route defines a command route.
type Route struct {
	Command     string
	Description string
	Aliases     []string
}

// Router dispatches commands to registered handlers.
type Router struct {
	routes   map[string]*Route
	agentOps AgentOps
}

// NewRouter creates a Router with built-in commands.
func NewRouter(ops AgentOps) *Router {
	r := &Router{
		routes:   make(map[string]*Route),
		agentOps: ops,
	}
	r.registerBuiltins()
	return r
}

// Register adds a command route.
func (r *Router) Register(route Route) {
	r.routes[route.Command] = &route
	for _, alias := range route.Aliases {
		r.routes[alias] = &route
	}
}

// Dispatch routes a message. Returns (result, handled).
// handled=false means the message is not a command and should be sent to the LLM.
func (r *Router) Dispatch(ctx context.Context, sessionID, msg string) (string, bool) {
	msg = strings.TrimSpace(msg)
	if !strings.HasPrefix(msg, "/") {
		return "", false
	}

	parts := strings.Fields(msg)
	if len(parts) == 0 {
		return "", false
	}

	cmd := strings.ToLower(parts[0][1:]) // strip leading "/"
	_, ok := r.routes[cmd]
	if !ok {
		return "Unknown command. Type /help for available commands.", true
	}

	switch cmd {
	case "new", "clear":
		if r.agentOps != nil {
			content, err := r.agentOps.HandleNewSession(ctx, sessionID)
			if err != nil {
				return "Failed to start new session: " + err.Error(), true
			}
			return content, true
		}
		return "New session started.", true

	case "stop":
		if r.agentOps != nil {
			content, err := r.agentOps.HandleStop(ctx, sessionID)
			if err != nil {
				return "Failed to stop: " + err.Error(), true
			}
			return content, true
		}
		return "Processing stopped.", true

	case "help":
		return r.buildHelp(), true

	case "status":
		return "nanobot is running.", true

	case "skills":
		return "Use the read_file tool to browse available skills in the skills directory.", true

	case "tools":
		return r.buildTools(), true

	default:
		return "Unknown command. Type /help for available commands.", true
	}
}

func (r *Router) buildHelp() string {
	var lines []string
	lines = append(lines, "Available commands:")
	seen := map[string]bool{}
	for _, route := range r.routes {
		if !seen[route.Command] {
			seen[route.Command] = true
			lines = append(lines, "  /"+route.Command+" — "+route.Description)
		}
	}
	return strings.Join(lines, "\n")
}

func (r *Router) buildTools() string {
	infos := r.agentOps.GetToolInfos()
	if len(infos) == 0 {
		return "No tools available."
	}

	var lines []string
	lines = append(lines, "Available tools:")
	for _, t := range infos {
		lines = append(lines, "")
		desc := t.Description
		if desc == "" {
			desc = "(no description)"
		}
		lines = append(lines, "  /"+t.Name+" — "+desc)

		if params := formatToolParams(t.Parameters); params != "" {
			lines = append(lines, "    Parameters:")
			lines = append(lines, params)
		}
	}
	return strings.Join(lines, "\n")
}

func formatToolParams(schema map[string]any) string {
	props, ok := schema["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		return ""
	}

	required := map[string]bool{}
	if reqList, ok := schema["required"].([]any); ok {
		for _, r := range reqList {
			if name, ok := r.(string); ok {
				required[name] = true
			}
		}
	}

	var lines []string
	for name, raw := range props {
		prop, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		desc := ""
		if d, _ := prop["description"].(string); d != "" {
			desc = " — " + d
		}
		mark := "  (optional)"
		if required[name] {
			mark = "  (required)"
		}
		typeStr := "string"
		if t, _ := prop["type"].(string); t != "" {
			typeStr = t
		}
		lines = append(lines, "      "+name+": "+typeStr+mark+desc)
	}
	return strings.Join(lines, "\n")
}

func (r *Router) registerBuiltins() {
	r.Register(Route{Command: "new", Aliases: []string{"clear"}, Description: "Start a new conversation"})
	r.Register(Route{Command: "stop", Description: "Stop the current processing"})
	r.Register(Route{Command: "help", Description: "Show available commands"})
	r.Register(Route{Command: "status", Description: "Show current status"})
	r.Register(Route{Command: "skills", Description: "List available skills"})
	r.Register(Route{Command: "tools", Description: "List available tools"})
}
