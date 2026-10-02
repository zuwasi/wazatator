package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
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

	if mode == "error" {
		fmt.Println(`{"type":"result","subtype":"success","is_error":true,"result":"Failed to authenticate","session_id":"sess-err"}`)
		return 1
	}
	_ = os.WriteFile(filepath.Join(cwd, "greeting.txt"), []byte("Ahoy"), 0o644)
	fmt.Print(claudeFixtureStream)
	return 0
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

func TestClaudeEngine_ExecuteReportsCLIError(t *testing.T) {
	e, _ := newFakeClaudeEngine(t, "error")

	resp, err := e.Execute(context.Background(), &ExecutionRequest{Message: "hi", NoSkills: true})
	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.Equal(t, "Failed to authenticate", resp.ErrorMsg)
	assert.Equal(t, "sess-err", resp.SessionID)
}

func TestClaudeEngine_RejectsUnsupportedFeatures(t *testing.T) {
	e := NewClaudeEngine("")
	_, err := e.Execute(context.Background(), &ExecutionRequest{Message: "x", Tools: []copilot.Tool{{Name: "t"}}})
	assert.ErrorContains(t, err, "custom tools")
	_, err = e.Execute(context.Background(), &ExecutionRequest{Message: "x", MCPServers: map[string]copilot.MCPServerConfig{"m": nil}})
	assert.ErrorContains(t, err, "MCP")
}
