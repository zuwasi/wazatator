package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/microsoft/waza/internal/jsonrpc"
	"github.com/microsoft/waza/internal/models"
)

// claudeToolServerName is the MCP server name used to expose in-process
// custom tools (ExecutionRequest.Tools) to Claude Code. Claude sees them as
// mcp__waza__<tool>.
const claudeToolServerName = "waza"

// claudeToolBridge serves ExecutionRequest.Tools over MCP streamable HTTP on
// 127.0.0.1 so Claude Code can call the in-process Go handlers (for example
// the prompt grader's set_waza_grade_pass / set_waza_grade_fail tools).
type claudeToolBridge struct {
	srv   *http.Server
	url   string
	token string
	tools map[string]copilot.Tool
	order []string
}

func startClaudeToolBridge(tools []copilot.Tool) (*claudeToolBridge, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("claude-cli: starting tool bridge: %w", err)
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		_ = ln.Close()
		return nil, err
	}
	b := &claudeToolBridge{
		url:   "http://" + ln.Addr().String() + "/mcp",
		token: hex.EncodeToString(tokenBytes),
		tools: make(map[string]copilot.Tool, len(tools)),
	}
	for _, t := range tools {
		b.tools[t.Name] = t
		b.order = append(b.order, t.Name)
	}
	b.srv = &http.Server{Handler: http.HandlerFunc(b.serveHTTP)}
	go func() { _ = b.srv.Serve(ln) }()
	return b, nil
}

func (b *claudeToolBridge) close() { _ = b.srv.Close() }

// mcpServerConfig returns the Claude --mcp-config entry for this bridge.
func (b *claudeToolBridge) mcpServerConfig() map[string]any {
	return map[string]any{
		"type":    "http",
		"url":     b.url,
		"headers": map[string]string{"Authorization": "Bearer " + b.token},
	}
}

func (b *claudeToolBridge) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+b.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed) // no server-initiated SSE stream
		return
	}
	var req jsonrpc.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, &jsonrpc.Response{JSONRPC: "2.0", Error: jsonrpc.ErrParseError(err.Error())})
		return
	}
	if len(req.ID) == 0 || string(req.ID) == "null" { // notification
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, b.handle(r.Context(), &req))
}

func (b *claudeToolBridge) handle(_ context.Context, req *jsonrpc.Request) *jsonrpc.Response {
	resp := &jsonrpc.Response{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-03-26"
		}
		resp.Result = map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": claudeToolServerName, "version": "1.0.0"},
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		list := make([]map[string]any, 0, len(b.order))
		for _, name := range b.order {
			t := b.tools[name]
			schema := t.Parameters
			if schema == nil {
				schema = map[string]any{"type": "object"}
			}
			list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": schema})
		}
		resp.Result = map[string]any{"tools": list}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = jsonrpc.ErrInvalidParams(err.Error())
			return resp
		}
		t, ok := b.tools[p.Name]
		if !ok || t.Handler == nil {
			resp.Error = jsonrpc.ErrInvalidParams("unknown tool " + p.Name)
			return resp
		}
		result, err := t.Handler(copilot.ToolInvocation{ToolName: p.Name, Arguments: p.Arguments})
		text, isError := result.TextResultForLLM, result.ResultType == "failure" || result.Error != ""
		if err != nil {
			text, isError = err.Error(), true
		} else if result.Error != "" {
			text = result.Error
		}
		if text == "" {
			text = "ok"
		}
		resp.Result = map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isError,
		}
	default:
		resp.Error = jsonrpc.ErrMethodNotFound(req.Method)
	}
	return resp
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// claudeMCPConfig converts Copilot SDK MCP server configs into the JSON
// accepted by `claude --mcp-config`.
func claudeMCPConfig(servers map[string]copilot.MCPServerConfig) (map[string]any, error) {
	out := make(map[string]any, len(servers))
	for name, cfg := range servers {
		switch c := cfg.(type) {
		case copilot.MCPStdioServerConfig:
			if c.WorkingDirectory != "" {
				return nil, fmt.Errorf("claude-cli: mcp server %q: cwd is not supported by Claude Code", name)
			}
			out[name] = map[string]any{"type": "stdio", "command": c.Command, "args": c.Args, "env": c.Env}
		case copilot.MCPHTTPServerConfig:
			out[name] = map[string]any{"type": "http", "url": c.URL, "headers": c.Headers}
		default:
			return nil, fmt.Errorf("claude-cli: mcp server %q has unsupported config type %T", name, cfg)
		}
	}
	return out, nil
}

// claudeToolsForPolicy maps a .agent.md tool policy onto Claude Code tool
// names. builtins is the value for `--tools` ("" disables all built-in tools);
// allowed is the lower-cased set of every Claude tool name the policy permits,
// used to fail closed on any call that slips through.
func claudeToolsForPolicy(p *ToolPolicy) (builtins string, allowed map[string]bool) {
	allowed = map[string]bool{}
	if p.Mode == ToolPolicyDenyAll {
		return "", allowed
	}
	var names []string
	add := func(claudeNames ...string) {
		for _, n := range claudeNames {
			if !allowed[strings.ToLower(n)] {
				names = append(names, n)
				allowed[strings.ToLower(n)] = true
			}
		}
	}
	for _, declared := range p.declared {
		lower := strings.ToLower(strings.TrimSpace(declared))
		switch {
		case strings.HasPrefix(lower, "mcp:"):
			// mcp:server/tool → mcp__server__tool; mcp:server → whole server.
			allowed["mcp__"+strings.ReplaceAll(strings.TrimPrefix(lower, "mcp:"), "/", "__")] = true
			continue
		case strings.HasPrefix(lower, "custom:"):
			allowed["mcp__"+claudeToolServerName+"__"+strings.TrimPrefix(lower, "custom:")] = true
			continue
		}
		switch models.CanonicalToolName(declared) {
		case "read":
			add("Read", "Glob", "Grep")
		case "write":
			add("Write", "Edit", "NotebookEdit")
		case "bash":
			add("Bash", "PowerShell")
		case "fetch":
			add("WebFetch")
		default:
			add(strings.TrimPrefix(strings.TrimSpace(declared), "builtin:"))
		}
	}
	sort.Strings(names)
	return strings.Join(names, ","), allowed
}

// claudePolicyDenials reports tool calls that the allowed set does not cover.
// MCP names match a whole-server grant (mcp__server) or an exact tool grant.
func claudePolicyDenials(calls []models.ToolCall, allowed map[string]bool) []ToolPolicyDenial {
	var denials []ToolPolicyDenial
	for _, c := range calls {
		name := strings.ToLower(c.Name)
		if allowed[name] {
			continue
		}
		if parts := strings.SplitN(name, "__", 3); len(parts) == 3 && parts[0] == "mcp" && allowed["mcp__"+parts[1]] {
			continue
		}
		denials = append(denials, ToolPolicyDenial{
			Tool:   c.Name,
			Kind:   "tool",
			Reason: fmt.Sprintf("tool %q is not declared in the agent's `tools:` allow-list", c.Name),
		})
	}
	return denials
}
