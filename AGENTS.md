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
- Create files unless explicitly asked
- Guess URLs or paths
- Use `cd && command` patterns
- Skip reading before editing
- Amend commits not created in session

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
- **REQUIRED**: Use `--trailer 'Co-authored-by: <AI-Model-Name> <<model-contact>>'` (search git log for previous commits with `Co-authored-by` to find exact format used)

## Skills

### Always Parse
- **ALWAYS read `SKILLS.md`** at the start of each session to load domain-specific instructions and workflows
- Use the `read` tool to load SKILLS.md (it is a regular file, not a registered skill)

---

*This file should be updated as new patterns and best practices are learned.*
