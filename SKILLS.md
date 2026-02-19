# Skills & Workflows

## General Guidelines

- Always review and follow the [BEP 15](https://bittorrent.org/beps/bep_0015.html) document as source of truth

## Language-Specific Guidelines

### Go
- No unnecessary comments
- No trailing periods on comments
- Trust initialization patterns (e.g., maps created by constructors)
- Use `go vet`, `gofmt`, and `staticcheck`
- Keep methods focused and minimal
- Follows best go practices
- Remove not used dependencies, parameters and variables

## Workflows

### Making Changes
1. Read relevant files
2. Understand existing patterns
3. Make minimal, focused edits
4. Verify with tests/linting
5. Confirm with user

### Debugging
1. Read error messages carefully
2. Check related files
3. Look for pattern mismatches
4. Test incrementally

### Code Review
- Check for security issues
- Verify no secrets exposed
- Ensure following conventions
- Test if possible

## Verification

### Before Completing
- [ ] Code compiles/builds
- [ ] Tests pass (if applicable)
- [ ] Linting passes
- [ ] No secrets in files
- [ ] Follows project conventions

### Commands to Run
- Go: `go build`, `go vet`, `gofmt`, `go mod tidy`, `golangci-lint run --timeout=5m`
- **CRITICAL**: `golangci-lint run --timeout=5m` must pass before any commit
