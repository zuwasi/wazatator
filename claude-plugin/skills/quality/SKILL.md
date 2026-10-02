---
name: quality
description: Reviews an Agent Skill's SKILL.md with Wazatator's static readiness check and an LLM quality score (clarity, completeness, trigger precision, scope, anti-patterns). Use when the user asks how good a skill is, wants a skill reviewed, or wants it ready to publish.
argument-hint: "[skill directory]"
---

# Review a skill's quality

Target skill directory: $ARGUMENTS (if empty, use the current directory).

1. Run `wazatator --version`. If it is missing, give the install commands from `/wazatator:eval` and stop.
2. Run the free static check (compliance, token budget, eval presence):

   ```bash
   wazatator check <skill-dir>
   ```

3. Run the LLM quality review through Claude Code (spends tokens):

   ```bash
   wazatator quality <skill-dir> --model sonnet
   ```

4. Summarize both: overall score, the weakest dimensions, and the specific lines in `SKILL.md` that cause them.
5. Propose concrete edits. If the user accepts, apply them and re-run step 3 to show the new score.
