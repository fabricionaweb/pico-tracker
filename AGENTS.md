# Agent Instructions

## Code Style & Conventions

### General Principles
- **Be concise**: Use minimal output while maintaining helpfulness
- **No preamble/postamble**: Avoid phrases like "Here is the..." or "Based on..."
- **Direct answers**: Answer questions directly without elaboration unless asked
- **Follow existing patterns**: Mimic code style, use existing libraries, follow conventions

## Tool Usage

### When to Use Each Tool

#### Read
- Always read files before editing
- Check file contents for context
- Use `offset/limit` for large files

#### Edit
- Preserve exact indentation
- Make minimal changes
- Use `replaceAll` for renames only

#### Bash
- Explain what commands do
- Use `workdir` instead of `cd`
- Quote paths with spaces

#### Grep/Glob
- Use for finding patterns
- Prefer specific patterns over broad ones

### Tool Combinations
- Read → Edit (always read first)
- Grep + Glob (parallel search)
- Multiple Bash commands (parallel when independent)

## Communication

### Response Style
1. **One-word answers** when possible
2. **Max 4 lines** unless user asks for detail
3. **No emojis** unless requested
4. **Code references**: Use `file_path:line_number` format

### Proactiveness
- Be proactive when asked to do something
- Don't surprise user with unexpected actions
- Ask before committing or pushing

## Security

### Never
- Commit secrets or credentials
- Skip hooks unless explicitly requested
- Force push to main/master
- Amend commits that were pushed

### Always
- Check files for malicious content
- Follow security best practices
- Protect user data

## Anti-Patterns

### Don't
- Add unnecessary comments
- Remove or modify existing comments (preserve all user-written comments)
- Refactor or "clean up" code unless explicitly asked
- Create files unless explicitly asked
- Guess URLs or paths
- Use `cd && command` patterns
- Skip reading before editing
- Amend commits not created in session
- **Make anything up** - always verify facts before stating them

### Do
- Add necessary comments
- Use tools for file operations
- Verify file paths exist
- Check git status before operations
- Ask when unclear
- Follow existing code style exactly

## Session Management

### Context
- Working directory: Check current path
- Git repo: Verify status before operations
- Platform: Respect OS differences

### Committing
- Only commit when explicitly asked
- Check for untracked files
- Verify no secrets before committing
- Use conventional commit format: `<type>: <description>` (e.g., `feat:`, `fix:`, `refactor:`, `docs:`, `chore:`)
- **ALWAYS run lint before committing**: Run `golangci-lint run` and fix any issues before committing
- **REQUIRED**: Add a co-author trailer for yourself: `git commit -m "<message>" --trailer "Co-authored-by: <Name> <<Email>>"`
  - **Step 1:** Identify your exact model name **and version** (e.g., Kimi K25, GPT-4o, Claude 3.5 Sonnet). Do not just use the broad family name.
  - **Step 2:** Search the git log to find your established format for this specific version. Run: `git log --grep="Co-authored-by:.*<Your-Exact-Version>" -n 6 --format="%(trailers:key=Co-authored-by)"`
  - **Step 3:** - *If found:* Use the exact string returned to maintain consistency.
    - *If not found (first time use):* Default to `Co-authored-by: <Your-Exact-Version> <<model-identifier>@ai-agent.local>` (e.g., `Co-authored-by: Kimi K25 <kimi-k25@ai-agent.local>`).

## Linting

### Before Committing (MANDATORY)
- **MUST** run `golangci-lint run --timeout=5m` and fix ALL issues before committing
- **MUST NOT** commit code with lint errors - this will fail CI
- **MUST NOT** add new lint issues to the codebase
- If linting fails, fix the issues before proceeding with any commit

### Why Strict?
CI runs the exact same lint check. Any lint error that passes locally will fail in CI. Always verify with `golangci-lint run --timeout=5m` before considering work complete.

### Config
- This project uses golangci-lint (see `.golangci.yml`)
- Check the config file for enabled linters and exclusions
- When adding workarounds for lint issues, use nolint comments or config exclusions (see existing patterns in the codebase)

### Modifying golangci-lint Config (IMPORTANT)
- **NEVER** modify `.golangci.yml` without explicit user instruction
- When facing lint errors, follow this priority:
  1. **Fix the code** - Address the underlying issue properly
  2. **Add nolint comment** - Use `//nolint:<linter>` with a detailed reason explaining why the exception is justified
  3. **Only if absolutely necessary** - Ask user before modifying config
- Exception: Only `*_test.go` exclusions are acceptable without explicit approval
- Prefer inline comments over config changes to keep context with the code
- Examples of good nolint comments:
  - `//nolint:gosec // G115: Protocol requires 32-bit field per BEP 15`
  - `//nolint:govet // Field alignment optimized for memory layout`

## Skills

### Always Parse
- **ALWAYS read `SKILLS.md`** at the start of each session to load domain-specific instructions and workflows
- Use the `read` tool to load SKILLS.md (it is a regular file, not a registered skill)

---

*This file should be updated as new patterns and best practices are learned.*
