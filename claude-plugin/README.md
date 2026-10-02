# Wazatator plugin for Claude Code

Evaluate and improve your Agent Skills from inside Claude Code.

| Skill | What it does |
|---|---|
| `/wazatator:new-eval [skill]` | Creates an eval suite (tasks, graders, trigger tests) for a skill |
| `/wazatator:eval [skill or eval.yaml]` | Runs the suite through Claude Code, explains failures, and offers fixes |
| `/wazatator:quality [skill]` | Static readiness check plus an LLM quality score for `SKILL.md` |

Claude also uses these skills on its own when you ask it to test or review a skill.

## Install

1. Install the `wazatator` CLI:
   - Windows PowerShell: `irm https://raw.githubusercontent.com/zuwasi/wazatator/main/install.ps1 | iex`
   - macOS/Linux: `curl -fsSL https://raw.githubusercontent.com/zuwasi/wazatator/main/install.sh | bash`
2. In Claude Code:

   ```
   /plugin marketplace add zuwasi/wazatator
   /plugin install wazatator@wazatator
   ```

## Cost

Every eval task and LLM-judged grader runs a real Claude Code session on your account. Keep `trials_per_task: 1` while iterating.

## Attribution

Wazatator is based on [Microsoft Waza](https://github.com/microsoft/waza) (MIT). It is not affiliated with or endorsed by Microsoft.
