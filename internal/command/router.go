package command

import (
	"context"
	"strings"
)

// Handler is a command handler function.
type Handler func(ctx context.Context, args []string) (string, error)

// Route defines a command route.
type Route struct {
	Command     string
	Description string
	Handler     Handler
	Aliases     []string
}

// Router dispatches commands to registered handlers.
type Router struct {
	routes map[string]*Route
}

// NewRouter creates a CommandRouter.
func NewRouter() *Router {
	r := &Router{routes: make(map[string]*Route)}
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

// Dispatch routes a message and returns the handler result.
// Returns nil if the message is not a command (no "/" prefix).
func (r *Router) Dispatch(ctx context.Context, msg string) (string, error) {
	msg = strings.TrimSpace(msg)
	if !strings.HasPrefix(msg, "/") {
		return "", nil
	}

	parts := strings.Fields(msg)
	if len(parts) == 0 {
		return "", nil
	}

	cmd := strings.ToLower(parts[0][1:]) // strip leading "/"
	route, ok := r.routes[cmd]
	if !ok {
		return "Unknown command. Type /help for available commands.", nil
	}

	return route.Handler(ctx, parts[1:])
}

// ListRoutes returns all registered commands.
func (r *Router) ListRoutes() []Route {
	seen := make(map[string]bool)
	var routes []Route
	for _, route := range r.routes {
		if !seen[route.Command] {
			seen[route.Command] = true
			routes = append(routes, *route)
		}
	}
	return routes
}

func (r *Router) registerBuiltins() {
	r.Register(Route{
		Command:     "help",
		Description: "Show available commands",
		Handler: func(ctx context.Context, args []string) (string, error) {
			var lines []string
			lines = append(lines, "Available commands:")
			for _, route := range r.ListRoutes() {
				lines = append(lines, "  /"+route.Command+" — "+route.Description)
			}
			return strings.Join(lines, "\n"), nil
		},
	})

	r.Register(Route{
		Command:     "new",
		Aliases:     []string{"clear"},
		Description: "Start a new conversation",
		Handler: func(ctx context.Context, args []string) (string, error) {
			return "Starting a new conversation. Previous context has been cleared.", nil
		},
	})

	r.Register(Route{
		Command:     "stop",
		Description: "Stop the current processing",
		Handler: func(ctx context.Context, args []string) (string, error) {
			return "Processing stopped.", nil
		},
	})

	r.Register(Route{
		Command:     "status",
		Description: "Show current status",
		Handler: func(ctx context.Context, args []string) (string, error) {
			return "nanobot is running.", nil
		},
	})

	r.Register(Route{
		Command:     "skills",
		Description: "List available skills",
		Handler: func(ctx context.Context, args []string) (string, error) {
			return "Skills list (use the tools to browse skills).", nil
		},
	})
}
