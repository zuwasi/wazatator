---
name: eval
description: Runs a Wazatator eval suite for an Agent Skill through Claude Code and explains the results. Use when the user asks to evaluate, benchmark, or test a skill (SKILL.md), check whether a skill edit helped, or compare two eval runs.
argument-hint: "[skill directory or eval.yaml]"
---

# Run a Wazatator eval

Target: $ARGUMENTS (if empty, use the skill in the current directory).

## 1. Check the CLI

Run `wazatator --version`. If it is not found, stop and tell the user to install it:

- Windows PowerShell: `irm https://raw.githubusercontent.com/zuwasi/wazatator/main/install.ps1 | iex`
- macOS/Linux: `curl -fsSL https://raw.githubusercontent.com/zuwasi/wazatator/main/install.sh | bash`

## 2. Find the eval spec

Look for an eval file in this order: the path the user gave, `eval.yaml` next to the skill's `SKILL.md`, then `evals/<skill-name>/eval.yaml`. If none exists, tell the user and offer `/wazatator:new-eval`.

Read the eval file. Confirm `config.executor` is `claude-cli` (the default when omitted) and note `trials_per_task`.

## 3. Run it

Each task starts a real Claude Code session and spends the user's tokens. Tell the user how many tasks and trials will run before starting.

Run from the directory that contains the skill so Wazatator can find it:

```bash
wazatator run <eval.yaml> -o <eval-dir>/results.json
```

Add `--context-dir <dir>` when the tasks reference fixture files outside the default `fixtures/` folder. Runs take minutes: use a 10-minute timeout or run it in the background.

Two optional modes, when the user asks for them:

- **What does the skill add?** Add `--baseline`. Every task also runs with all skills off, and the report shows the per-task difference. A task that passes without the skill isn't evidence the skill helps.
- **Does it clash with my other skills?** Add `--skill-library <dir>` (for example `~/.claude/skills`), repeatable. Those skills compete for the same prompts, and trigger tests list prompts another skill took. Make sure `trigger_skill_routing` is off in `eval.yaml` for this.

## 4. Explain the results

Read `results.json` and report:

- Each task: pass/fail, and for failures the grader name and its `feedback`.
- `trigger_metrics` when trigger tests ran (precision, recall).
- Token usage from the summary.

For each failure, read that run's `final_output`, `tool_events`, and `skill_invocations` to find the cause. Common causes: the skill was never invoked, the agent asked a question instead of acting, or the output missed required content. Separate skill problems from unrealistic task prompts or graders.

## 5. Offer a fix

Propose concrete `SKILL.md` edits for skill problems. If the user accepts, apply them, re-run with a new output file, and compare:

```bash
wazatator compare <old-results.json> <new-results.json>
```

Report what changed. Do not claim a fix worked without a passing re-run.
