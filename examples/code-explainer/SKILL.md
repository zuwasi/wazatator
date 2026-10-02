---
name: code-explainer
description: |
  **UTILITY SKILL** - Explain code snippets, functions, and algorithms in plain language.
  USE FOR: explain code, what does this code do, break down this function,
  help me understand this, walk through this algorithm, clarify this logic,
  explain this snippet, describe what happens here.
  DO NOT USE FOR: writing new code (use code generation), fixing bugs (use debugging),
  refactoring (use refactoring skills), code review with action items.
  INVOKES: file reading tools to access code, language detection for tailored explanations.
  FOR SINGLE OPERATIONS: If the user just needs to see file contents, use file reading tools directly.
---

# Code Explainer

A skill that explains code snippets in clear, educational language. Helps developers understand unfamiliar code, algorithms, and programming patterns.

## Skill Activation Triggers

**Use this skill immediately when the user asks to:**
- "Explain this code"
- "What does this function do?"
- "Break down this algorithm"
- "Help me understand this snippet"
- "Walk me through this code"
- "What's happening in this file?"

**Key Indicators:**
- User shares a code snippet and asks for explanation
- User is confused about existing code
- User wants to learn how something works
- User asks "what does X do?" about code

**Do NOT use when:**
- User wants to write new code
- User wants to fix a bug
- User wants code review with suggestions
- User wants refactoring recommendations

## Quick Reference

| Property | Value |
|----------|-------|
| Input | Code snippet + optional language hint |
| Output | Plain language explanation |
| Best for | Learning, onboarding, code comprehension |

## Finding the Code

If the user refers to "this code" but no snippet is in the message, do not ask for the code right away:

1. Use code from the conversation, or files the user attached or selected.
2. Otherwise list the working directory. If there is exactly one source file, or one clearly matching the request, read it and explain it, and say which file you explained.
3. Ask the user for the code only when there is no candidate file or several equally likely ones.
## Explanation Guidelines

### Structure

A good code explanation should include:

1. **Overview** - One sentence summary of what the code does
2. **Step-by-step breakdown** - Walk through the logic
3. **Key concepts** - Explain any patterns or techniques used
4. **Example** - Show sample input/output if applicable

### Tone

- Educational and approachable
- Avoid jargon unless explaining it
- Use analogies when helpful
- Assume the reader is smart but unfamiliar with this specific code

### Language-Specific Tips

| Language | Focus On |
|----------|----------|
| Python | List comprehensions, decorators, generators |
| JavaScript | Async/await, closures, callbacks |
| SQL | JOINs, subqueries, aggregations |
| Java | OOP patterns, generics, streams |

## Examples

### Example 1: Python Recursion

**User prompt:**
```
Explain this code:
```python
def factorial(n):
    if n <= 1:
        return 1
    return n * factorial(n - 1)
```

**Good response:**
> This is a **recursive function** that calculates the factorial of a number.
>
> **Overview:** It multiplies a number by all positive integers less than it (e.g., 5! = 5 × 4 × 3 × 2 × 1 = 120).
>
> **Step by step:**
> 1. **Base case** (`if n <= 1`): If n is 0 or 1, return 1 (since 0! and 1! both equal 1)
> 2. **Recursive case**: Otherwise, multiply n by factorial(n-1)
>
> **Example:** `factorial(4)` → 4 × factorial(3) → 4 × 3 × factorial(2) → 4 × 3 × 2 × factorial(1) → 4 × 3 × 2 × 1 = 24

### Example 2: JavaScript Async/Await

**User prompt:**
```
What does this code do?
```javascript
async function fetchUserData(userId) {
  try {
    const response = await fetch(`/api/users/${userId}`);
    if (!response.ok) throw new Error('User not found');
    return await response.json();
  } catch (error) {
    console.error('Failed:', error);
    return null;
  }
}
```

**Good response:**
> This is an **async function** that fetches user data from an API.
>
> **Overview:** It makes an HTTP request to get a user's data, handles errors gracefully, and returns the result as JSON.
>
> **Step by step:**
> 1. `async function` - Declares this function will use await for promises
> 2. `await fetch(...)` - Makes HTTP GET request, waits for response
> 3. `if (!response.ok)` - Checks if request succeeded (status 200-299)
> 4. `await response.json()` - Parses response body as JSON
> 5. `catch` block - If anything fails, logs error and returns null
>
> **Key pattern:** This is the standard async/await pattern for API calls with error handling.

## Error Cases

If the user provides:
- **No code**: Ask them to share the code they want explained
- **Incomplete code**: Explain what you can see and note what's missing
- **Non-code text**: Clarify you need actual code to explain

## Integration Notes

This skill works well with:
- File reading tools (to access code from files)
- Language detection (to tailor explanations)
- Search tools (to find related documentation)
