---
name: clean-code
description: Defines the code style for the Anti-Fraud project: function length, helpers, naming, comments, and the procedure for cleaning up existing code. Use before writing, reviewing, or refactoring any Go, TypeScript, or Python code.
---

# Clean Code

## 1. Purpose

This document defines how code is written in the Anti-Fraud project, so that every function can be read in one pass and the three codebases look like the work of one team.

It is used in two ways:

- **Writing:** new and changed code follows sections 3 to 8.
- **Cleanup:** existing code is brought in line with the same rules by the procedure in section 9.

## 2. Scope

| Covered | Location |
|---|---|
| Go | `backend/`, including tests |
| TypeScript and React | `frontend/src/` |
| Python | `ml/`, including tests |

The following are never restyled:

- Merged migrations in `backend/migrations/`.
- Test data and model files: `ml/testdata/`, `backend/models/`.

Formatting is not covered here. It belongs to the tools: `gofmt` and `go vet` for Go, `oxlint` and the TypeScript check for the frontend.

## 3. Functions

### 3.1 Length

A function is counted from its signature line to its closing line, both included.

| Length | Rule |
|---|---|
| 40 lines or fewer | The target |
| 41 to 60 lines | Allowed when splitting would not make the function easier to read |
| More than 60 lines | Must be split into helpers |

One exception: a function may exceed 60 lines when the extra length is a single flat list with no logic in it, such as one struct literal, one field-by-field mapping, or one `switch` with a line per case. A comment above the function says so in one line.

### 3.2 One Job

- A function does one thing, at one level of detail.
- A function that coordinates several steps reads as the list of those steps. The steps themselves live in helpers. `ServeHTTP` should read as: decode, validate, load, decide, save, respond.
- A function either computes a value or changes something. When it does both, the name says so: `respondWithSavedDecision`.

### 3.3 Shape

- Handle errors and special cases first and return early. The main path is not indented.
- No more than three levels of nesting. A deeper block becomes a helper.
- No `else` after a branch that returns.
- No more than four parameters. Go's `ctx` and a test's `t` do not count. More than four become a struct or an options object.
- No boolean parameter that switches between two behaviours. Write two functions.

## 4. Helpers

### 4.1 When to Extract

| Sign | Action |
|---|---|
| A block has a comment that says what it does | The block becomes a helper and the comment becomes its name |
| A function is over the limit in section 3.1 | Extract each step |
| The same logic appears in three places | Extract one helper. Two similar blocks may stay |
| A condition needs explaining | Extract it into a named boolean or a small function: `isTrustedDevice(p)` |

### 4.2 When Not to Extract

- A helper used once that needs more than four parameters or returns more than three values. The cut is in the wrong place; look for a different step boundary.
- A one-line wrapper that only renames a call.
- A helper shared by two callers that need different behaviour, switched by a flag.

### 4.3 Placement and Naming

- A helper is private to its file or package and is placed below the first function that calls it.
- Code moves to a shared location only when a second package needs it. In Go that location is `internal/platform`.
- No files, packages, or modules named `utils`, `helpers`, or `common`. Name the file after what it contains: `displayFormat.ts`.
- A helper is named after what it returns or does: `ruleInput`, `loadHistory`, `writeScoreResponse`. Never `process`, `handle`, `doWork`, or a numbered name.

## 5. Naming

- Follow each language's convention: `MixedCaps` in Go, `camelCase` and `PascalCase` components in TypeScript, `snake_case` in Python.
- Short names for short scopes (`p`, `rs`, `err`), full words for anything visible across a file or package.
- A name carries its unit when the type does not: `AmountMinor`, `WindowDays`, `Uses1h`.
- A boolean reads as a statement: `blocked`, `enabled`, `isNew`.
- Abbreviations are limited to the established ones: `ID`, `IP`, `OS`, `URL`, `ctx`, `err`, `req`, `resp`.
- One concept has one name everywhere. A purchase is not also called a payment, an order, and a transaction.

## 6. Comments

A comment explains why the code is the way it is. The code itself shows what it does.

| Write | Do not write |
|---|---|
| `// Model inputs are computed for every purchase, blocked ones too, so that all saved purchases can be exported for training.` | `// Compute the model inputs` |
| `// Save has rolled back, so the lookup can see the committed winner.` | `// Check the error` |

- Comments are complete sentences that end with a period.
- A Go exported type or function has a doc comment that starts with its name. A plain constructor such as `NewStore` may go without one.
- A comment states a constraint the reader cannot see in the code: a required call order, a unit, a reason for an unusual choice.
- No commented-out code. Git keeps the old version.
- No `TODO` or `FIXME`. Create a Jira task instead.
- No comments about the history of a change, such as "new", "fixed", or "moved from". That belongs in the commit.
- No banner or divider comments that split a file into sections. Split the file.
- No mention of AI tools or assistants.

## 7. Errors

- Go: wrap an error with the action that failed, in lowercase, without "failed to": `fmt.Errorf("read player history: %w", err)`.
- An error is handled once: either returned or logged, never both.
- Python: no bare `except`, and no `except Exception` that hides the error.
- TypeScript: no empty `catch`. A caught error is shown to the user or thrown again.

## 8. Language Rules

### 8.1 Go

- `internal/scoring` stays pure: no SQL, no HTTP, no clock. It receives everything as arguments.
- A handler method only decodes, calls, and responds. Queries and decisions live in the store and in `scoring`.
- A query and the scan of its row into a domain type live in one function per query: `playerHistory(ctx, in, at)`. A function that needs several queries only calls those.
- SQL text is a named constant next to the store that runs it.
- No naked returns and no named results, except to document two results of the same type.

### 8.2 TypeScript and React

- Plain functions follow section 3.1. A component is counted the same way, with a higher limit because of its markup:

| Component length | Rule |
|---|---|
| 80 lines or fewer | The target |
| More than 150 lines | Must be split |

- Split a component by extracting child components for parts of the markup, and custom hooks for state and effects that belong together, as `usePolling` does.
- Code that does not use hooks or props lives outside the component as a plain function: `toRequest(values)`.
- Props that a component receives for events are named `onX`. The function that handles an event is named with a verb for what it does: `save`, `retry`, `openQueue`.
- No `any`. No nested ternaries in markup.

### 8.3 Python

- `main` only parses arguments and calls step functions in order. Each step, such as loading, training, evaluating, and writing a report, is its own function that takes its inputs as arguments and returns its results.
- Building the argument parser is its own function.
- No work at module level other than imports and constants. Scripts start from `if __name__ == "__main__":`.
- A report dictionary is built by a function that returns it, not assembled across a long `main`.

### 8.4 Tests

- A test covers one behaviour, and its name states that behaviour: `TestScoreRejectsInvalidRequests`.
- The table of cases in a table-driven test does not count toward the limit in section 3.1. The rest of the test does.
- Setup and assertions repeated in three tests move to a helper. In Go the helper calls `t.Helper()`.
- A test reads in three parts: arrange, act, assert. Expected values are written out in the test, not computed by the code under test.

## 9. Cleanup Procedure

Cleanup changes how the code is arranged and never what it does.

### 9.1 Before Starting

- The working tree is clean and the work is on its own branch, as `commit-style` requires.
- The checks in section 9.4 pass. A failure that exists before the cleanup is reported and not fixed as part of it.

### 9.2 Steps

1. Work on one unit at a time: one Go package, one frontend file, or one Python file.
2. List every function in the unit that breaks a rule, longest first, with its length.
3. Present a plan to the developer: for each function, the helpers to extract and their names.
4. Wait for approval, then make the changes.
5. Run the checks in section 9.4.
6. Commit the unit on its own with a `refactor:` message, following `commit-style`.

### 9.3 What Must Not Change

- API requests and responses, status codes, and error messages.
- SQL queries, the schema, and the order of database calls inside a transaction.
- Exported names, JSON field names, log messages, and command-line flags.
- Model inputs and outputs. The golden rows in `ml/testdata/` and `backend/models/` must still match exactly.
- Test expectations. A test that has to change is a sign that behaviour changed.

A bug found during cleanup is reported and left alone. It gets its own task and its own `fix:` commit.

A function with no test covering it is reported before it is refactored, so that the developer can decide whether to add a test first.

### 9.4 Checks

| Area | Commands |
|---|---|
| Go | `make lint` and `make test` |
| Frontend | `npm run check` and `npm run build` in `frontend/` |
| Python | `pytest` in `ml/` |

The results after the cleanup must be the same as before it.

## 10. Checklist

- No function over 60 lines, and no component over 150, without the exception in section 3.1.
- No more than three levels of nesting and four parameters.
- Every helper has a name that says what it returns or does.
- Every comment explains why, and none restates the code.
- No commented-out code, `TODO`, or `utils` file.
- The checks in section 9.4 pass.
