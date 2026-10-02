---
name: new-eval
description: Creates a Wazatator eval suite (eval.yaml, tasks, trigger tests) for an Agent Skill. Use when the user wants to start testing a skill that has no evals, or asks for new test cases for a SKILL.md.
argument-hint: "[skill directory or skill name]"
---

# Create a Wazatator eval suite

Target skill: $ARGUMENTS (if empty, use the skill in the current directory).

1. Run `wazatator --version`. If it is missing, give the install commands from `/wazatator:eval` and stop.
2. Read the skill's `SKILL.md`: its purpose, trigger phrases, and anything it must not handle.
3. From the directory that contains the skill, scaffold the suite:

   ```bash
   wazatator new eval <skill-name>
   ```

   This creates `evals/<skill-name>/eval.yaml` and starter tasks under `tasks/`.
4. Check `eval.yaml` has `executor: claude-cli`, `model: sonnet`, `trials_per_task: 1`, and `inject_skill_body: false` under `config`. Without `inject_skill_body: false`, Claude reads the skill from the system prompt, never calls its Skill tool, and skill-invocation graders and trigger tests fail.
5. Replace the starter tasks with 3 to 5 realistic tasks:
   - Tasks a real user would type, including one vague prompt (for example "explain this" with a file in `fixtures/`).
   - Put input files in `fixtures/` next to `eval.yaml` and list them under `inputs.files` with paths relative to `fixtures/`.
   - Use this task shape; every key shown is required where present:

     ```yaml
     id: vague-with-file-001
     name: Vague request with a file
     inputs:
       prompt: "explain this"
       files:
         - path: example.py
     expected:
       output_contains: ["must-have phrase"]
     graders:
       - name: used-skill
         type: skill_invocation
         config:
           required_skills: [<skill-name>]
           mode: any_order
       - name: judge
         type: prompt
         config:
           model: haiku
           continue_session: true
           prompt: |-
             Review your previous answer in this conversation. If it meets <criteria>, call set_waza_grade_pass. Otherwise call set_waza_grade_fail with your reasoning.
     ```

   - Keep `continue_session: true` on prompt graders that judge the answer: without it the judge sees only the workspace files, not the conversation.

   - For a task that must not use the skill, use `forbidden_skills: [<skill-name>]` instead of `required_skills`.
6. Write `trigger_tests.yaml` next to `eval.yaml` with at least 3 `should_trigger_prompts` and 3 `should_not_trigger_prompts`, including near-misses the skill must not claim. Each entry is an object, not a bare string:

   ```yaml
   skill: <skill-name>
   should_trigger_prompts:
     - prompt: "a request the skill should handle"
       confidence: high
   should_not_trigger_prompts:
     - prompt: "a near-miss the skill must not claim"
       confidence: high
   ```
7. Show the user the files you created and offer `/wazatator:eval` to run them. Grader configuration errors only appear when the suite runs, so say the suite is untested until that first run.
