---
name: jira-task-creator
description: Defines how work items are written and created in the Anti-Fraud Jira project (AF). Use before creating, editing, or planning any Jira epic, story, task, bug, or subtask.
---

# Jira Task Creator

## 1. Purpose

This document defines how work items are structured, written, and created in the Anti-Fraud Jira project, so that the backlog stays consistent, estimable, and easy to review.

## 2. Project

| Item | Value |
|---|---|
| Site | nasirlinijatdev.atlassian.net |
| Project | Anti-Fraud (`AF`) |
| Framework | Scrum, one-week sprints |
| Board columns | To Do → In Progress → In Review → Done |

## 3. Work Item Hierarchy

| Level | Type | Use for | Example |
|---|---|---|---|
| 1 | Epic | A large feature area spanning several stories | Rules Engine |
| 2 | Story | A piece of user-facing value that fits in one sprint | Analyst sees flagged payments in a list |
| 2 | Task | Technical work with no direct user-facing value | Set up CI pipeline |
| 2 | Bug | A defect in existing behaviour | Negative amounts are approved |
| 3 | Subtask | An optional technical step inside a story, task, or bug | Write handler tests |

Every story, task, and bug belongs to an epic.

## 4. Writing Rules

### 4.1 Summary

- Short and specific, no more than 80 characters.
- Starts with a verb for tasks and subtasks: `Set up CI pipeline`.
- Describes the outcome for stories: `Analyst sees flagged payments in a list`.
- No trailing period.

### 4.2 Description Templates

**Story**

```
As a <role>, I want <capability>, so that <benefit>.

Acceptance criteria
- <verifiable condition>
- <verifiable condition>

Notes
- <optional technical notes>
```

**Task**

```
Goal
<what must be achieved>

Definition of done
- <verifiable condition>
```

**Bug**

```
Steps to reproduce
1. <step>

Expected result
<what should happen>

Actual result
<what happens>
```

### 4.3 Estimation

- Stories, tasks, and bugs are estimated in story points using the Fibonacci scale: 1, 2, 3, 5, 8.
- An item estimated above 8 points must be split into smaller items.
- Epics and subtasks are not estimated.

### 4.4 Assignment

- New items are left unassigned. Team members assign themselves during sprint planning.

### 4.5 Language

- Work items are written in English, in a neutral and professional tone.
- Work items must not mention AI tools or assistants.

## 5. Procedure

1. Search the project for existing items with similar summaries to avoid duplicates.
2. Present the proposed items as a table: type, summary, parent epic, story points, sprint.
3. Wait for explicit approval from the project owner.
4. Create the items only after approval is given: epics first, then their children.
5. Report the keys of the created items (for example `AF-12`).

## 6. Restrictions

The following require explicit approval from the project owner, given separately for each change:

- Editing or deleting existing items.
- Moving items between statuses or sprints.
- Starting or completing a sprint.
- Changing project, board, or workflow settings.
