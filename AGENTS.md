# AGENTS.md

This file provides guidance to AI coding assistants working in this repository.

## 1. Project Overview

Anti-Fraud is a payment risk-scoring service modeled on the Xsolla Anti-fraud system. Every incoming payment is evaluated in three stages:

| Stage | Check | Possible outcome |
|---|---|---|
| 1. Blocklist | User, email, IP, or card is blocked | Decline |
| 2. Rules | Velocity, amount limit, region lock, country mismatch | Review or Decline |
| 3. ML model | Fraud probability score | Approve, Review, or Decline |

Payments marked for review are handled by analysts in a review dashboard.

| Item | Value |
|---|---|
| Deadline | 2026-11-17 |
| Jira | Project `AF` on nasirlinijatdev.atlassian.net (Scrum, one-week sprints) |
| Repository | `nasirlinijat/anti-fraud` (private) |

## 2. Technology Stack

| Component | Technology | Location | Status |
|---|---|---|---|
| Backend | Go, standard library `net/http` | `backend/` | Initialized |
| Frontend | React, JavaScript | `frontend/` | Not started |
| ML model | LightGBM, IEEE-CIS Fraud Detection dataset | — | Not started |

## 3. Repository Structure

```
anti-fraud/
├── AGENTS.md                 This file
├── CLAUDE.md                 Imports this file for Claude Code
├── .agent/
│   └── skills/               Project skills (see section 5)
└── backend/
    ├── go.mod                Module github.com/nasirlinijat/anti-fraud/backend
    └── cmd/server/main.go    HTTP server entry point
```

## 4. Development

Run the backend:

```bash
cd backend
go run ./cmd/server
```

The server listens on the port given by the `PORT` environment variable (default `8080`) and exposes `GET /health`.

## 5. Skills

Read the relevant skill before performing the matching task.

| Skill | File | Use before |
|---|---|---|
| Commit style | `.agent/skills/commit-style/SKILL.md` | Creating any git commit |

## 6. General Rules

1. Implement only what was requested. Do not scaffold or add unrequested components.
2. Never mention AI tools or assistants in code or documentation.
3. ML features must use only fields that the API receives at request time.
