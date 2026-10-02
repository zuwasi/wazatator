package execution

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/microsoft/waza/internal/copilotevents"
	"github.com/microsoft/waza/internal/models"
)

// ClaudeEngine runs tasks through the Claude Code CLI (`claude -p` with
// stream-json output). The CLI's NDJSON stream is translated into Copilot SDK
// shaped session events so the existing transcript, tool-call, and
// skill-invocation pipeline works unchanged.
//
// The target skill is copied into the task's .claude/skills directory so
// Claude Code discovers it natively and can invoke it with its Skill tool.
type ClaudeEngine struct {
	defaultModelID string
	cliPath        string
	keepWorkspace  bool

	mu           sync.Mutex
	workspaces   []string
	gitResources []GitResource
	usage        map[string]*models.UsageStats
}

// NewClaudeEngine creates an engine that shells out to the Claude Code CLI.
// The binary defaults to `claude` on PATH; set CLAUDE_CLI_PATH to override.
func NewClaudeEngine(defaultModelID string) *ClaudeEngine {
	cliPath := os.Getenv("CLAUDE_CLI_PATH")
	if cliPath == "" {
		cliPath = "claude"
	}
	return &ClaudeEngine{
		defaultModelID: defaultModelID,
		cliPath:        cliPath,
		usage:          map[string]*models.UsageStats{},
	}
}

// NewAuxiliaryEngine builds the engine for commands outside `waza run`
// (quality, suggest, spec verify, dev, tokens suggest). Set
// WAZA_EXECUTOR=claude-cli to use Claude Code instead of the Copilot SDK.
func NewAuxiliaryEngine(modelID string) AgentEngine {
	if os.Getenv("WAZA_EXECUTOR") == "claude-cli" {
		return NewClaudeEngine(modelID)
	}
	return NewCopilotEngineBuilder(modelID, nil).Build()
}

// claudeModelName converts Copilot-style Claude model names
// ("claude-sonnet-4.6") to Claude Code's form ("claude-sonnet-4-6") so evals
// and defaults written for copilot-sdk also work with claude-cli.
func claudeModelName(model string) string {
	if strings.HasPrefix(model, "claude-") {
		return strings.ReplaceAll(model, ".", "-")
	}
	return model
}

// SetKeepWorkspace enables or disables workspace preservation on shutdown.
func (e *ClaudeEngine) SetKeepWorkspace(keep bool) { e.keepWorkspace = keep }

// Initialize verifies the Claude Code CLI can be found.
func (e *ClaudeEngine) Initialize(ctx context.Context) error {
	if _, err := exec.LookPath(e.cliPath); err != nil {
		return fmt.Errorf("claude-cli executor: Claude Code CLI %q not found (install Claude Code or set CLAUDE_CLI_PATH): %w", e.cliPath, err)
	}
	return nil
}

func (e *ClaudeEngine) Execute(ctx context.Context, req *ExecutionRequest) (*ExecutionResponse, error) {
	if req == nil {
		return nil, errors.New("nil req was passed to ClaudeEngine.Execute")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	start := time.Now()

	workspaceDir := req.WorkspaceDir
	if workspaceDir == "" {
		var err error
		if workspaceDir, err = e.setupWorkspace(ctx, req.Resources, req.GitResources); err != nil {
			return nil, err
		}
	}
	workingDir, err := ResolveWorkDir(workspaceDir, req.WorkDir)
	if err != nil {
		return nil, err
	}

	sourceDir := req.SourceDir
	if sourceDir == "" {
		if sourceDir, err = os.Getwd(); err != nil {
			return nil, fmt.Errorf("failed to get current directory: %w", err)
		}
	}

	var systemParts []string
	var skillCopyRel string
	if !req.NoSkills {
		skillDirs := append([]string{sourceDir}, req.SkillPaths...)
		if skillCopyRel, err = installClaudeSkill(skillDirs, req.SkillName, workspaceDir, workingDir); err != nil {
			return nil, err
		}
		if msg := buildSkillSystemMessage(skillDirs, req.SkillName, !req.SuppressSkillBody); msg != "" {
			systemParts = append(systemParts, msg)
		}
		if msg := buildTriggerSkillRoutingSystemMessage(req.SkillName, req.TriggerSkillRouting && req.SuppressSkillBody); msg != "" {
			systemParts = append(systemParts, msg)
		}
	}
	if msg := buildInstructionSystemMessage(req.Instructions); msg != "" {
		systemParts = append(systemParts, msg)
	}

	modelID := e.defaultModelID
	if req.ModelID != "" {
		modelID = req.ModelID
	}

	// Skip user-level settings (personal plugins, skills, hooks) and MCP
	// connectors so results depend on the skill under test, not the machine.
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "bypassPermissions",
		"--setting-sources", "project,local", "--strict-mcp-config"}
	if modelID != "" {
		args = append(args, "--model", claudeModelName(modelID))
	}
	if req.ReasoningEffort != "" {
		args = append(args, "--effort", req.ReasoningEffort)
	}
	if req.SessionID != "" {
		args = append(args, "--resume", req.SessionID)
	} else if req.EphemeralSession {
		args = append(args, "--no-session-persistence")
	}
	if len(systemParts) > 0 {
		args = append(args, "--append-system-prompt", strings.Join(systemParts, "\n"))
	}

	var policyAllowed map[string]bool
	if req.ToolPolicy.Active() {
		var builtins string
		builtins, policyAllowed = claudeToolsForPolicy(req.ToolPolicy)
		args = append(args, "--tools", builtins)
	}

	mcpServers, err := claudeMCPConfig(req.MCPServers)
	if err != nil {
		return nil, err
	}
	if len(req.Tools) > 0 {
		bridge, err := startClaudeToolBridge(req.Tools)
		if err != nil {
			return nil, err
		}
		defer bridge.close()
		mcpServers[claudeToolServerName] = bridge.mcpServerConfig()
	}
	if len(mcpServers) > 0 {
		cfgFile, err := writeClaudeMCPConfig(mcpServers)
		if err != nil {
			return nil, err
		}
		defer os.Remove(cfgFile)
		args = append(args, "--mcp-config", cfgFile)
	}

	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	cmd := exec.CommandContext(runCtx, e.cliPath, args...)
	cmd.Dir = workingDir
	cmd.Stdin = strings.NewReader(req.Message) // stdin avoids Windows command-line length/quoting limits
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude-cli: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude-cli: failed to start %q: %w", e.cliPath, err)
	}
	var stdout io.Reader = pipe
	if req.FirstEventTimeout > 0 {
		stdout = newFirstOutputWatchdog(pipe, req.FirstEventTimeout, func() { cancel(errFirstEventTimeout) })
	}

	collector := NewSessionEventsCollector()
	canceledForSkill := false
	if req.CancelOnSkillInvocation {
		collector.SetOnSkillInvoked(func(SkillInvocation) {
			canceledForSkill = true
			cancel(nil)
		})
	}
	collector.On(newClaudeEvent(&copilot.UserMessageData{Content: req.Message}))

	stream := parseClaudeStream(stdout, workingDir, collector.On)
	waitErr := cmd.Wait()

	errMsg := stream.errMsg
	switch {
	case canceledForSkill:
		errMsg = ""
	case errors.Is(context.Cause(runCtx), errFirstEventTimeout):
		errMsg = fmt.Sprintf("session start timeout: no first turn within %s (engine launched but produced no events): %v", req.FirstEventTimeout, errFirstEventTimeout)
	case ctx.Err() != nil:
		errMsg = ctx.Err().Error()
	case errMsg == "" && waitErr != nil:
		errMsg = strings.TrimSpace(fmt.Sprintf("claude exited: %v %s", waitErr, stderr.String()))
	case errMsg == "" && !stream.sawResult:
		errMsg = "claude produced no result event: " + strings.TrimSpace(stderr.String())
	}

	var workspaceFiles map[string][]byte
	if !req.SkipWorkspaceCapture {
		workspaceFiles = captureWorkspaceFiles(workspaceDir)
		if skillCopyRel != "" {
			for k := range workspaceFiles {
				if strings.HasPrefix(k, skillCopyRel+"/") {
					delete(workspaceFiles, k)
				}
			}
		}
	}

	if stream.model != "" {
		modelID = stream.model
	}
	sessionID := stream.sessionID
	if sessionID == "" {
		sessionID = req.SessionID
	}
	if sessionID != "" && stream.usage != nil && !req.EphemeralSession {
		e.mu.Lock()
		e.usage[sessionID] = stream.usage
		e.mu.Unlock()
	}

	resp := &ExecutionResponse{
		FinalOutput:      joinStrings(collector.OutputParts()),
		Events:           copilotevents.FromSDK(collector.SessionEvents()),
		ModelID:          modelID,
		SkillInvocations: collector.SkillInvocations,
		DurationMs:       time.Since(start).Milliseconds(),
		ToolCalls:        collector.ToolCalls(),
		ErrorMsg:         errMsg,
		Success:          errMsg == "",
		WorkspaceDir:     workspaceDir,
		WorkspaceFiles:   workspaceFiles,
		SessionID:        sessionID,
		Usage:            stream.usage,
	}
	if req.ToolPolicy != nil {
		resp.ToolPolicyMode = string(req.ToolPolicy.Mode)
	}
	if policyAllowed != nil {
		if denials := claudePolicyDenials(resp.ToolCalls, policyAllowed); len(denials) > 0 {
			resp.ToolPolicyDenials = denials
			if resp.Success {
				resp.Success = false
				resp.ErrorMsg = fmt.Sprintf("tool policy violation: %d tool call(s) denied by .agent.md `tools:` policy", len(denials))
			}
		}
	}
	return resp, nil
}

// writeClaudeMCPConfig writes servers as a `claude --mcp-config` JSON file
// (a file avoids Windows command-line quoting issues) and returns its path.
func writeClaudeMCPConfig(servers map[string]any) (string, error) {
	data, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		return "", fmt.Errorf("claude-cli: encoding mcp config: %w", err)
	}
	f, err := os.CreateTemp("", "waza-claude-mcp-*.json")
	if err != nil {
		return "", fmt.Errorf("claude-cli: writing mcp config: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", fmt.Errorf("claude-cli: writing mcp config: %w", err)
	}
	return f.Name(), nil
}

// firstOutputWatchdog calls onTimeout unless the wrapped reader yields data
// within the timeout; it distinguishes a CLI that never starts its turn from a
// legitimately long one.
type firstOutputWatchdog struct {
	r     io.Reader
	timer *time.Timer
	once  sync.Once
}

func newFirstOutputWatchdog(r io.Reader, timeout time.Duration, onTimeout func()) *firstOutputWatchdog {
	return &firstOutputWatchdog{r: r, timer: time.AfterFunc(timeout, onTimeout)}
}

func (w *firstOutputWatchdog) Read(p []byte) (int, error) {
	n, err := w.r.Read(p)
	if n > 0 || err != nil {
		w.once.Do(func() { w.timer.Stop() })
	}
	return n, err
}

// Shutdown removes workspaces and git resources. Safe to call multiple times.
func (e *ClaudeEngine) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	workspaces, gitResources := e.workspaces, e.gitResources
	e.workspaces, e.gitResources = nil, nil
	e.mu.Unlock()

	for _, gr := range gitResources {
		if err := gr.Cleanup(ctx); err != nil {
			slog.Warn("failed to cleanup git resource", "error", err)
		}
	}
	for _, ws := range workspaces {
		if e.keepWorkspace {
			fmt.Fprintf(os.Stderr, "Workspace preserved: %s\n", ws)
		} else if err := os.RemoveAll(ws); err != nil {
			slog.Warn("failed to cleanup workspace", "path", ws, "error", err)
		}
	}
	return nil
}

// SessionUsage returns the usage reported by the CLI's final result event.
func (e *ClaudeEngine) SessionUsage(sessionID string) *models.UsageStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.usage[sessionID]
}

func (e *ClaudeEngine) setupWorkspace(ctx context.Context, resources []ResourceFile, gitResources []models.GitResource) (string, error) {
	workspaceDir, err := os.MkdirTemp("", "waza-claude-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp workspace: %w", err)
	}
	e.mu.Lock()
	e.workspaces = append(e.workspaces, workspaceDir)
	e.mu.Unlock()

	if err := setupWorkspaceResources(workspaceDir, resources); err != nil {
		return "", fmt.Errorf("failed to setup resources at workspace %s: %w", workspaceDir, err)
	}
	gitRes, err := CloneGitResources(ctx, gitResources, workspaceDir)
	if err != nil {
		return "", fmt.Errorf("failed to materialize git resources at workspace %s: %w", workspaceDir, err)
	}
	e.mu.Lock()
	e.gitResources = append(e.gitResources, gitRes...)
	e.mu.Unlock()
	return workspaceDir, nil
}

// installClaudeSkill copies the target SKILL.md directory into
// <workingDir>/.claude/skills/<name> so Claude Code discovers it as a project
// skill. It returns the copy's workspace-relative slash path ("" when nothing
// was installed) so callers can exclude it from captured workspace files.
func installClaudeSkill(skillDirs []string, skillName, workspaceDir, workingDir string) (string, error) {
	sd, err := findSkillDefinition(skillDirs, skillName)
	if err != nil || sd == nil || filepath.Base(sd.Path) != "SKILL.md" {
		return "", err
	}
	dst := filepath.Join(workingDir, ".claude", "skills", sd.Name)
	rel, err := filepath.Rel(workspaceDir, dst)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dst); err == nil {
		return filepath.ToSlash(rel), nil // follow-up turn reusing the workspace
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", fmt.Errorf("creating claude skills dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte(sd.Content), 0o644); err != nil {
		return "", fmt.Errorf("copying skill %q into workspace: %w", sd.Name, err)
	}
	// Copy only the optional Agent Skills directories, not eval artifacts
	// (tasks, fixtures, results) that often live next to SKILL.md.
	for _, sub := range []string{"scripts", "references", "assets"} {
		src := filepath.Join(sd.Dir, sub)
		if info, err := os.Stat(src); err != nil || !info.IsDir() {
			continue
		}
		if err := os.CopyFS(filepath.Join(dst, sub), os.DirFS(src)); err != nil {
			return "", fmt.Errorf("copying skill %q %s: %w", sd.Name, sub, err)
		}
	}
	return filepath.ToSlash(rel), nil
}

// claudeStreamResult holds the session-level data extracted from the stream.
type claudeStreamResult struct {
	sessionID string
	model     string
	usage     *models.UsageStats
	errMsg    string
	sawResult bool
}

// claudeStreamLine is the subset of Claude Code stream-json fields we use.
type claudeStreamLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	Message   struct {
		Content []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     map[string]any  `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		} `json:"content"`
	} `json:"message"`
	IsError    bool             `json:"is_error"`
	Result     string           `json:"result"`
	NumTurns   int              `json:"num_turns"`
	Usage      claudeTokenUsage `json:"usage"`
	ModelUsage map[string]struct {
		InputTokens              int     `json:"inputTokens"`
		OutputTokens             int     `json:"outputTokens"`
		CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
		CostUSD                  float64 `json:"costUSD"`
	} `json:"modelUsage"`
}

type claudeTokenUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// parseClaudeStream reads Claude Code stream-json from r and emits
// Copilot-shaped session events. Non-JSON lines are ignored.
func parseClaudeStream(r io.Reader, workingDir string, emit func(copilot.SessionEvent)) claudeStreamResult {
	var res claudeStreamResult
	br := bufio.NewReader(r)
	for {
		raw, readErr := br.ReadBytes('\n')
		if line := bytes.TrimSpace(raw); len(line) > 0 && line[0] == '{' {
			var l claudeStreamLine
			if err := json.Unmarshal(line, &l); err != nil {
				slog.Debug("claude-cli: skipping unparsable stream line", "error", err)
			} else {
				handleClaudeLine(&l, workingDir, emit, &res)
			}
		}
		if readErr != nil {
			return res
		}
	}
}

func handleClaudeLine(l *claudeStreamLine, workingDir string, emit func(copilot.SessionEvent), res *claudeStreamResult) {
	if l.SessionID != "" {
		res.sessionID = l.SessionID
	}
	switch l.Type {
	case "system":
		if l.Subtype == "init" && l.Model != "" {
			res.model = l.Model
		}
	case "assistant":
		for _, c := range l.Message.Content {
			switch c.Type {
			case "text":
				if c.Text != "" {
					emit(newClaudeEvent(&copilot.AssistantMessageData{Content: c.Text}))
				}
			case "tool_use":
				args := c.Input
				if fp, ok := args["file_path"]; ok {
					if _, has := args["path"]; !has {
						args["path"] = fp // match Waza's ToolCallArgs.Path for file tools
					}
				}
				emit(newClaudeEvent(&copilot.ToolExecutionStartData{ToolCallID: c.ID, ToolName: c.Name, Arguments: args}))
				if c.Name == "Skill" {
					if name := claudeSkillName(args); name != "" {
						path := filepath.Join(workingDir, ".claude", "skills", name, "SKILL.md")
						if _, err := os.Stat(path); err != nil {
							path = ""
						}
						emit(newClaudeEvent(&copilot.SkillInvokedData{Name: name, Path: path}))
					}
				}
			}
		}
	case "user":
		for _, c := range l.Message.Content {
			if c.Type == "tool_result" && c.ToolUseID != "" {
				emit(newClaudeEvent(&copilot.ToolExecutionCompleteData{
					ToolCallID: c.ToolUseID,
					Success:    !c.IsError,
					Result:     &copilot.ToolExecutionCompleteResult{Content: claudeToolResultText(c.Content)},
				}))
			}
		}
	case "result":
		res.sawResult = true
		res.usage = claudeUsage(l)
		if l.IsError || strings.HasPrefix(l.Subtype, "error") {
			res.errMsg = l.Result
			if res.errMsg == "" {
				res.errMsg = "claude run failed: " + l.Subtype
			}
			emit(newClaudeEvent(&copilot.SessionErrorData{ErrorType: l.Subtype, Message: res.errMsg}))
		} else {
			emit(newClaudeEvent(&copilot.SessionIdleData{}))
		}
	}
}

// claudeSkillName extracts the skill name from a Skill tool input, stripping
// any plugin namespace ("plugin:skill").
func claudeSkillName(input map[string]any) string {
	for _, key := range []string{"skill", "command"} {
		if s, ok := input[key].(string); ok && s != "" {
			s = strings.TrimPrefix(s, "/")
			if i := strings.LastIndex(s, ":"); i >= 0 {
				s = s[i+1:]
			}
			return s
		}
	}
	return ""
}

// claudeToolResultText flattens a tool_result content field, which is either
// a string or a list of {type:"text", text:"..."} blocks.
func claudeToolResultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

func claudeUsage(l *claudeStreamLine) *models.UsageStats {
	u := &models.UsageStats{
		Turns:            l.NumTurns,
		InputTokens:      l.Usage.InputTokens,
		OutputTokens:     l.Usage.OutputTokens,
		CacheReadTokens:  l.Usage.CacheReadInputTokens,
		CacheWriteTokens: l.Usage.CacheCreationInputTokens,
		Provider:         "anthropic",
	}
	if len(l.ModelUsage) > 0 {
		u.ModelMetrics = make(map[string]models.ModelUsage, len(l.ModelUsage))
		for name, m := range l.ModelUsage {
			u.ModelMetrics[name] = models.ModelUsage{
				InputTokens:      m.InputTokens,
				OutputTokens:     m.OutputTokens,
				CacheReadTokens:  m.CacheReadInputTokens,
				CacheWriteTokens: m.CacheCreationInputTokens,
				RequestCost:      m.CostUSD,
			}
		}
	}
	return u
}

func newClaudeEvent(data copilot.SessionEventData) copilot.SessionEvent {
	return copilot.SessionEvent{Data: data, Timestamp: time.Now()}
}
