---
name: commit-style
description: Defines the commit message format and the commit procedure for the Anti-Fraud project. Use before creating any git commit.
---

# Commit Style

## 1. Purpose

This document defines how commits are written and created in the Anti-Fraud project, so that the history stays consistent, readable, and traceable to Jira tasks.

## 2. Message Format

Commit messages follow the [Conventional Commits](https://www.conventionalcommits.org/) specification:

```
<type>: <description>
```

### 2.1 Types

| Type | Use for |
|---|---|
| `feat` | A new feature |
| `fix` | A bug fix |
| `refactor` | A code change that neither adds a feature nor fixes a bug |
| `test` | Adding or updating tests |
| `docs` | Documentation only |
| `chore` | Tooling, configuration, dependencies |

### 2.2 Description Rules

- Written in lowercase, in the imperative mood: `add`, not `added` or `adds`.
- One line, no more than 72 characters.
- No trailing period.
- No message body and no trailers.
- No Jira keys or other issue references.

### 2.3 Examples

| Correct | Incorrect |
|---|---|
| `feat: add payment endpoint` | `Added payment endpoint.` |
| `fix: reject negative amounts` | `fix bug` |
| `test: cover velocity rule` | `feat: Tests` |

## 3. Authorship

- Every commit is authored solely by the developer using the assistant, with their own configured git identity.
- Commit messages must not mention AI tools or assistants.
- No `Co-Authored-By` or other attribution trailers may be added.

## 4. Procedure

No git command that changes the repository, including `git add`, may be run before approval.

1. Present a commit plan to the developer containing:
   - the files to be included,
   - the files that will be left out, if any,
   - the exact commands, in a code block,
   - the commit message.
2. Wait for explicit approval from the developer.
3. If the developer requests changes, update the plan and present it again.
4. Run the commands only after approval is given, exactly as presented.
5. Report the result: the commit hash, author, and message.

## 5. Pushing

The assistant never pushes. The developer decides which branch to push and performs every push personally.

## 6. Excluded Files

The following must never be staged:

- Editor and IDE folders: `.idea/`, `.vscode/`
- Secrets and local environment files: `.env`
- Build output: `bin/`
