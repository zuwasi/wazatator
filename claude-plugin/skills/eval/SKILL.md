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

Optional modes, when the user asks for them:

- **What does the skill add?** Add `--baseline`. Every task also runs with all skills off, and the report shows the per-task difference plus a paired bootstrap significance line (mean Δ, 95% CI, p). A task that passes without the skill isn't evidence the skill helps.
- **Does it help on every model?** Repeat `--model` (for example `--model haiku --model sonnet`) together with `--baseline`. The skill transfer matrix shows the skill's effect per model and flags negative transfer, where the skill makes a model worse.
- **Does it clash with my other skills?** Add `--skill-library <dir>` (for example `~/.claude/skills`), repeatable. Those skills compete for the same prompts, and trigger tests list prompts another skill took. Make sure `trigger_skill_routing` is off in `eval.yaml` for this.

Significance needs repeated samples: suggest `trials_per_task: 3` or more when the user wants to trust a difference.

## 4. Explain the results

Read `results.json` and report:

- Each task: pass/fail, and for failures the grader name and its `feedback`.
- `trigger_metrics` when trigger tests ran (precision, recall, and `collisions` with a skill library).
- `skill_impact_stats` with `--baseline`: say plainly whether the difference is significant. Do not present a non-significant gain as a win.
- Token usage from the summary.

For failures, sample up to 5 failing runs and up to 3 passing runs (read at most about 15k characters of each). Read each run's `final_output`, `tool_events`, and `skill_invocations`, and contrast what the passing runs did that the failing ones did not. Common causes: the skill was never invoked, the agent asked a question instead of acting, or the output missed required content. Separate skill problems from unrealistic task prompts or graders.

## 5. Offer a fix (gated, logged, reversible)

Propose concrete `SKILL.md` edits for skill problems. Prefer describing what the agent must decide and when, over low-level step lists. If the user accepts:

1. Keep the current results as the baseline (for example copy them to `old-results.json`) and back up `SKILL.md`.
2. Apply the edit and re-run to a new output file, appending to the impact log:

   ```bash
   wazatator run <eval.yaml> -o new-results.json --impact-log skill-impact.md
   ```

3. Gate the change. Exit code 0 means no regression:

   ```bash
   wazatator gate --baseline old-results.json --current new-results.json
   wazatator compare old-results.json new-results.json
   ```

4. Keep the edit only if the gate passes and the score beats the best so far. Otherwise restore the backed-up `SKILL.md` and say the edit was reverted.
5. Fill in the **Change** and **Decision** lines of the new `skill-impact.md` entry (what you changed, kept or reverted, and why).

If some tasks are tagged `holdout`, iterate with `--tags '!holdout'` and run the full suite once at the end. A fix that only improves the tasks you tuned against is overfitting.

Report what changed. Do not claim a fix worked without a passing re-run.
