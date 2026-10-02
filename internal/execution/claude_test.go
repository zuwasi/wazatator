package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/microsoft/waza/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claudeFixtureStream mirrors Claude Code `-p --output-format stream-json --verbose` output.
const claudeFixtureStream = `{"type":"system","subtype":"init","session_id":"sess-1","model":"claude-haiku-4-5","tools":["Skill","Write"]}
not json, e.g. a CLI warning
{"type":"assistant","session_id":"sess-1","message":{"content":[{"type":"tool_use","id":"tu1","name":"Skill","input":{"skill":"pirate-greeter"}}]}}
{"type":"user","session_id":"sess-1","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","content":"Launching skill: pirate-greeter"}]}}
{"type":"assistant","session_id":"sess-1","message":{"content":[{"type":"tool_use","id":"tu2","name":"Write","input":{"file_path":"greeting.txt","content":"Ahoy"}}]}}
{"type":"user","session_id":"sess-1","message":{"content":[{"type":"tool_result","tool_use_id":"tu2","content":[{"type":"text","text":"File created"}],"is_error":false}]}}
{"type":"assistant","session_id":"sess-1","message":{"content":[{"type":"text","text":"Ahoy, matey!"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"Ahoy, matey!","session_id":"sess-1","num_turns":3,"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":30,"cache_creation_input_tokens":40},"modelUsage":{"claude-haiku-4-5":{"inputTokens":10,"outputTokens":20,"cacheReadInputTokens":30,"cacheCreationInputTokens":40,"costUSD":0.01}}}
`

// TestMain lets the test binary act as a fake `claude` CLI when re-executed
// with WAZA_FAKE_CLAUDE set.
func TestMain(m *testing.M) {
	if mode := os.Getenv("WAZA_FAKE_CLAUDE"); mode != "" {
		os.Exit(fakeClaude(mode))
	}
	os.Exit(m.Run())
}

func fakeClaude(mode string) int {
	stdin, _ := io.ReadAll(os.Stdin)
	cwd, _ := os.Getwd()
	_, skillErr := os.Stat(filepath.Join(cwd, ".claude", "skills", "pirate-greeter", "SKILL.md"))
	logData, _ := json.Marshal(map[string]any{"args": os.Args[1:], "stdin": string(stdin), "skillInstalled": skillErr == nil})
	_ = os.WriteFile(os.Getenv("WAZA_FAKE_CLAUDE_LOG"), logData, 0o644)

	switch mode {
	case "error":
		fmt.Println(`{"type":"result","subtype":"success","is_error":true,"result":"Failed to authenticate","session_id":"sess-err"}`)
		return 1
	case "hang":
		time.Sleep(10 * time.Second)
		return 0
	case "judge":
		// Act like a judge model: call the waza grading tool over MCP.
		if err := fakeClaudeCallBridgeTool(os.Args[1:], "set_waza_grade_pass", map[string]any{"description": "d", "reason": "r"}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(`{"type":"assistant","session_id":"judge","message":{"content":[{"type":"tool_use","id":"j1","name":"mcp__waza__set_waza_grade_pass","input":{"description":"d","reason":"r"}}]}}`)
		fmt.Println(`{"type":"result","subtype":"success","is_error":false,"result":"graded","session_id":"judge","num_turns":1}`)
		return 0
	}
	_ = os.WriteFile(filepath.Join(cwd, "greeting.txt"), []byte("Ahoy"), 0o644)
	fmt.Print(claudeFixtureStream)
	return 0
}

// fakeClaudeCallBridgeTool reads the --mcp-config file and calls a tool on
// the "waza" HTTP MCP server, as Claude Code would.
func fakeClaudeCallBridgeTool(args []string, tool string, toolArgs map[string]any) error {
	var cfgPath string
	for i, a := range args {
		if a == "--mcp-config" && i+1 < len(args) {
			cfgPath = args[i+1]
		}
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	var cfg struct {
		MCPServers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	srv := cfg.MCPServers[claudeToolServerName]
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": toolArgs}})
	req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(body))
	for k, v := range srv.Headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bridge returned %s", resp.Status)
	}
	return nil
}

func newFakeClaudeEngine(t *testing.T, mode string) (*ClaudeEngine, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "fake-claude.json")
	t.Setenv("WAZA_FAKE_CLAUDE", mode)
	t.Setenv("WAZA_FAKE_CLAUDE_LOG", logPath)
	t.Setenv("CLAUDE_CLI_PATH", os.Args[0])
	e := NewClaudeEngine("haiku")
	require.NoError(t, e.Initialize(context.Background()))
	t.Cleanup(func() { _ = e.Shutdown(context.Background()) })
	return e, logPath
}

func writePirateSkill(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "pirate-greeter")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: pirate-greeter\ndescription: Greets like a pirate.\n---\nSay Ahoy.\n"), 0o644))
	return root
}

func TestParseClaudeStream(t *testing.T) {
	var events []copilot.SessionEvent
	res := parseClaudeStream(strings.NewReader(claudeFixtureStream), t.TempDir(), func(e copilot.SessionEvent) {
		events = append(events, e)
	})

	assert.Equal(t, "sess-1", res.sessionID)
	assert.Equal(t, "claude-haiku-4-5", res.model)
	assert.True(t, res.sawResult)
	assert.Empty(t, res.errMsg)
	require.NotNil(t, res.usage)
	assert.Equal(t, 3, res.usage.Turns)
	assert.Equal(t, 10, res.usage.InputTokens)
	assert.Equal(t, 20, res.usage.OutputTokens)
	assert.Equal(t, 30, res.usage.CacheReadTokens)
	assert.Equal(t, 40, res.usage.CacheWriteTokens)
	assert.InDelta(t, 0.01, res.usage.ModelMetrics["claude-haiku-4-5"].RequestCost, 1e-9)

	var types []copilot.SessionEventType
	for _, e := range events {
		types = append(types, e.Type())
	}
	assert.Equal(t, []copilot.SessionEventType{
		copilot.SessionEventTypeToolExecutionStart,
		copilot.SessionEventTypeSkillInvoked,
		copilot.SessionEventTypeToolExecutionComplete,
		copilot.SessionEventTypeToolExecutionStart,
		copilot.SessionEventTypeToolExecutionComplete,
		copilot.SessionEventTypeAssistantMessage,
		copilot.SessionEventTypeSessionIdle,
	}, types)
}

func TestInstallClaudeSkill_CopiesOnlySkillContent(t *testing.T) {
	root := writePirateSkill(t)
	skillDir := filepath.Join(root, "pirate-greeter")
	for _, f := range []string{"scripts/run.sh", "references/guide.md", "tasks/task.yaml", "results.json"} {
		p := filepath.Join(skillDir, f)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
	}
	ws := t.TempDir()

	rel, err := installClaudeSkill([]string{root}, "pirate-greeter", ws, ws)
	require.NoError(t, err)
	assert.Equal(t, ".claude/skills/pirate-greeter", rel)

	dst := filepath.Join(ws, ".claude", "skills", "pirate-greeter")
	assert.FileExists(t, filepath.Join(dst, "SKILL.md"))
	assert.FileExists(t, filepath.Join(dst, "scripts", "run.sh"))
	assert.FileExists(t, filepath.Join(dst, "references", "guide.md"))
	assert.NoDirExists(t, filepath.Join(dst, "tasks"))
	assert.NoFileExists(t, filepath.Join(dst, "results.json"))
}

func TestClaudeModelName(t *testing.T) {
	assert.Equal(t, "claude-sonnet-4-6", claudeModelName("claude-sonnet-4.6"))
	assert.Equal(t, "sonnet", claudeModelName("sonnet"))
	assert.Equal(t, "gpt-5.1", claudeModelName("gpt-5.1"))
}

func TestNewAuxiliaryEngine(t *testing.T) {
	t.Setenv("WAZA_EXECUTOR", "")
	assert.IsType(t, &ClaudeEngine{}, NewAuxiliaryEngine("sonnet"))
	t.Setenv("WAZA_EXECUTOR", "copilot-sdk")
	assert.IsType(t, &CopilotEngine{}, NewAuxiliaryEngine("sonnet"))
}

func TestClaudeSkillName(t *testing.T) {
	assert.Equal(t, "pdf", claudeSkillName(map[string]any{"skill": "pdf"}))
	assert.Equal(t, "pdf", claudeSkillName(map[string]any{"skill": "docs-plugin:pdf"}))
	assert.Equal(t, "pdf", claudeSkillName(map[string]any{"command": "/pdf"}))
	assert.Empty(t, claudeSkillName(map[string]any{}))
}

func TestClaudeEngine_Execute(t *testing.T) {
	e, logPath := newFakeClaudeEngine(t, "ok")
	skillRoot := writePirateSkill(t)

	resp, err := e.Execute(context.Background(), &ExecutionRequest{
		Message:    "Please greet me.",
		SkillName:  "pirate-greeter",
		SkillPaths: []string{skillRoot},
		SourceDir:  skillRoot,
		Resources:  []ResourceFile{{Path: "input.txt", Content: []byte("hello")}},
	})
	require.NoError(t, err)

	assert.True(t, resp.Success, resp.ErrorMsg)
	assert.Equal(t, "Ahoy, matey!", resp.FinalOutput)
	assert.Equal(t, "sess-1", resp.SessionID)
	assert.Equal(t, "claude-haiku-4-5", resp.ModelID)

	require.Len(t, resp.SkillInvocations, 1)
	assert.Equal(t, "pirate-greeter", resp.SkillInvocations[0].Name)
	assert.FileExists(t, resp.SkillInvocations[0].Path)

	require.Len(t, resp.ToolCalls, 2)
	assert.Equal(t, "Skill", resp.ToolCalls[0].Name)
	assert.Equal(t, "pirate-greeter", resp.ToolCalls[0].Arguments.Skill)
	assert.Equal(t, "Write", resp.ToolCalls[1].Name)
	assert.Equal(t, "greeting.txt", resp.ToolCalls[1].Arguments.Path)
	assert.True(t, resp.ToolCalls[1].Success)
	assert.Equal(t, "File created", resp.ToolCalls[1].Result.Content)

	assert.Equal(t, "Ahoy", string(resp.WorkspaceFiles["greeting.txt"]))
	assert.Equal(t, "hello", string(resp.WorkspaceFiles["input.txt"]))
	for k := range resp.WorkspaceFiles {
		assert.False(t, strings.HasPrefix(k, ".claude/skills/"), "installed skill leaked into workspace files: %s", k)
	}

	require.NotNil(t, resp.Usage)
	assert.Equal(t, 20, resp.Usage.OutputTokens)
	assert.Same(t, resp.Usage, e.SessionUsage("sess-1"))
	assert.Len(t, resp.ExtractMessages(), 1)

	var log struct {
		Args           []string `json:"args"`
		Stdin          string   `json:"stdin"`
		SkillInstalled bool     `json:"skillInstalled"`
	}
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &log))
	assert.Equal(t, "Please greet me.", log.Stdin)
	assert.True(t, log.SkillInstalled)
	joined := strings.Join(log.Args, " ")
	assert.Contains(t, joined, "-p --output-format stream-json --verbose")
	assert.Contains(t, joined, "--setting-sources project,local --strict-mcp-config")
	assert.Contains(t, joined, "--model haiku")
	assert.Contains(t, joined, "<skill_context>")
	assert.NotContains(t, joined, "--resume")

	// Follow-up turn resumes the session in the same workspace.
	follow, err := e.Execute(context.Background(), &ExecutionRequest{
		Message:      "Again.",
		SessionID:    resp.SessionID,
		WorkspaceDir: resp.WorkspaceDir,
		SkillName:    "pirate-greeter",
		SkillPaths:   []string{skillRoot},
		SourceDir:    skillRoot,
	})
	require.NoError(t, err)
	assert.True(t, follow.Success, follow.ErrorMsg)
	data, err = os.ReadFile(logPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &log))
	assert.Contains(t, strings.Join(log.Args, " "), "--resume sess-1")
}

// Trigger tests pass the eval directory as SourceDir; the skill may live next
// to the current directory instead, as it does for regular task runs.
func TestClaudeEngine_FindsSkillFromCurrentDirWhenSourceDirLacksIt(t *testing.T) {
	e, logPath := newFakeClaudeEngine(t, "ok")
	skillRoot := writePirateSkill(t)
	t.Chdir(skillRoot)

	_, err := e.Execute(context.Background(), &ExecutionRequest{
		Message:   "hi",
		SkillName: "pirate-greeter",
		SourceDir: t.TempDir(), // eval dir without the skill
	})
	require.NoError(t, err)

	var log struct {
		SkillInstalled bool `json:"skillInstalled"`
	}
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &log))
	assert.True(t, log.SkillInstalled)
}

func TestClaudeEngine_ExecuteReportsCLIError(t *testing.T) {
	e, _ := newFakeClaudeEngine(t, "error")

	resp, err := e.Execute(context.Background(), &ExecutionRequest{Message: "hi", NoSkills: true})
	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.Equal(t, "Failed to authenticate", resp.ErrorMsg)
	assert.Equal(t, "sess-err", resp.SessionID)
}

func readFakeClaudeArgs(t *testing.T, logPath string) []string {
	t.Helper()
	var log struct {
		Args []string `json:"args"`
	}
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &log))
	return log.Args
}

// TestClaudeEngine_CustomToolsViaMCPBridge covers prompt graders: in-process
// tool handlers must be callable by Claude through the MCP bridge.
func TestClaudeEngine_CustomToolsViaMCPBridge(t *testing.T) {
	e, logPath := newFakeClaudeEngine(t, "judge")
	var calls []map[string]any
	tool := copilot.Tool{
		Name:       "set_waza_grade_pass",
		Parameters: map[string]any{"type": "object"},
		Handler: func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
			calls = append(calls, inv.Arguments.(map[string]any))
			return copilot.ToolResult{}, nil
		},
	}

	resp, err := e.Execute(context.Background(), &ExecutionRequest{
		Message: "grade it", Tools: []copilot.Tool{tool}, NoSkills: true, EphemeralSession: true, SkipWorkspaceCapture: true,
	})
	require.NoError(t, err)
	assert.True(t, resp.Success, resp.ErrorMsg)
	require.Len(t, calls, 1)
	assert.Equal(t, "r", calls[0]["reason"])
	assert.Contains(t, strings.Join(readFakeClaudeArgs(t, logPath), " "), "--no-session-persistence")
	assert.Nil(t, e.SessionUsage("judge"), "ephemeral sessions are not tracked")
}

func TestClaudeEngine_ToolPolicyAndEffort(t *testing.T) {
	e, logPath := newFakeClaudeEngine(t, "ok")
	resp, err := e.Execute(context.Background(), &ExecutionRequest{
		Message:         "hi",
		NoSkills:        true,
		ReasoningEffort: "high",
		ToolPolicy:      NewToolPolicy(&[]string{"read"}),
	})
	require.NoError(t, err)

	joined := strings.Join(readFakeClaudeArgs(t, logPath), " ")
	assert.Contains(t, joined, "--tools Glob,Grep,Read")
	assert.Contains(t, joined, "--effort high")

	// The fake CLI still calls Skill and Write, which the policy forbids.
	assert.False(t, resp.Success)
	assert.Equal(t, string(ToolPolicyAllowList), resp.ToolPolicyMode)
	require.Len(t, resp.ToolPolicyDenials, 2)
	assert.Equal(t, "Skill", resp.ToolPolicyDenials[0].Tool)
	assert.Contains(t, resp.ErrorMsg, "tool policy violation")
}

func TestClaudeEngine_FirstEventTimeout(t *testing.T) {
	e, _ := newFakeClaudeEngine(t, "hang")
	start := time.Now()
	resp, err := e.Execute(context.Background(), &ExecutionRequest{Message: "hi", NoSkills: true, FirstEventTimeout: 300 * time.Millisecond})
	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.ErrorMsg, "session start timeout")
	assert.Less(t, time.Since(start), 8*time.Second)
}

func TestClaudeToolBridge(t *testing.T) {
	b, err := startClaudeToolBridge([]copilot.Tool{{
		Name:        "echo",
		Description: "echoes",
		Handler: func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
			return copilot.ToolResult{TextResultForLLM: "got " + inv.Arguments.(map[string]any)["x"].(string)}, nil
		},
	}})
	require.NoError(t, err)
	defer b.close()

	post := func(token string, body string) (*http.Response, map[string]any) {
		req, _ := http.NewRequest(http.MethodPost, b.url, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}

	resp, _ := post("wrong", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp, _ = post(b.token, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)

	_, out := post(b.token, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	assert.Equal(t, "2025-06-18", out["result"].(map[string]any)["protocolVersion"])

	_, out = post(b.token, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools := out["result"].(map[string]any)["tools"].([]any)
	require.Len(t, tools, 1)
	assert.Equal(t, map[string]any{"type": "object"}, tools[0].(map[string]any)["inputSchema"])

	_, out = post(b.token, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"x":"hi"}}}`)
	result := out["result"].(map[string]any)
	assert.Equal(t, false, result["isError"])
	assert.Equal(t, "got hi", result["content"].([]any)[0].(map[string]any)["text"])
}

func TestClaudeMCPConfig(t *testing.T) {
	cfg, err := claudeMCPConfig(map[string]copilot.MCPServerConfig{
		"local":  copilot.MCPStdioServerConfig{Command: "srv", Args: []string{"-x"}, Env: map[string]string{"A": "1"}, Tools: []string{"*"}},
		"remote": copilot.MCPHTTPServerConfig{URL: "https://example.test/mcp", Headers: map[string]string{"K": "V"}},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"type": "stdio", "command": "srv", "args": []string{"-x"}, "env": map[string]string{"A": "1"}}, cfg["local"])
	assert.Equal(t, map[string]any{"type": "http", "url": "https://example.test/mcp", "headers": map[string]string{"K": "V"}}, cfg["remote"])

	_, err = claudeMCPConfig(map[string]copilot.MCPServerConfig{"bad": copilot.MCPStdioServerConfig{Command: "x", WorkingDirectory: "/tmp"}})
	assert.ErrorContains(t, err, "cwd")
}

func TestClaudeToolsForPolicy(t *testing.T) {
	builtins, allowed := claudeToolsForPolicy(NewToolPolicy(&[]string{}))
	assert.Equal(t, "", builtins)
	assert.Empty(t, allowed)

	builtins, allowed = claudeToolsForPolicy(NewToolPolicy(&[]string{"view", "edit", "Bash", "mcp:github", "mcp:docs/search", "custom:my_tool"}))
	assert.Equal(t, "Bash,Edit,Glob,Grep,NotebookEdit,PowerShell,Read,Write", builtins)

	denials := claudePolicyDenials([]models.ToolCall{
		{Name: "Read"}, {Name: "mcp__github__create_issue"}, {Name: "mcp__docs__search"},
		{Name: "mcp__waza__my_tool"}, {Name: "mcp__docs__delete"}, {Name: "WebFetch"},
	}, allowed)
	var denied []string
	for _, d := range denials {
		denied = append(denied, d.Tool)
	}
	assert.Equal(t, []string{"mcp__docs__delete", "WebFetch"}, denied)
}
