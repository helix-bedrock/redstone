# AGENTS.md

Rules for AI coding agents working in this repo. Also read the org-wide rules in
`../.github/AGENT_RULES.md` (online:
<https://github.com/helix-bedrock/.github/blob/main/AGENT_RULES.md>). Where they
disagree, this file wins for this repo.

## What this repo is

The circuit system of Minecraft: Bedrock Edition, one-to-one with BDS 1.26.60. It is
a library, not part of the main engine, and must never be merged into it. It never
knows block IDs or names: the engine creates components and answers `World`.

The engine owns blocks: what each one makes, what it does when its power changes,
its placing and its sounds. In Helix that is the `circuit` package.

## Commands

```bash
go build ./...
go vet ./...
go test ./...
```

## Layout

| File | Owns |
|---|---|
| `types.go` | Positions, faces, kinds, `World` |
| `component.go` | What each kind of component does |
| `system.go` | The graph: adding, removing, finding paths, evaluating in phases |

## Where this differs from BDS on purpose

BDS keeps its chunks' components in a hash map and evaluates them in that map's
order. Here chunks run in the order their first component was added, so the order
is repeatable. Only circuits spread over several chunks could notice.

## Status

Status lives in [STATUS.md](STATUS.md) under "Status as of <date>"; refresh it with
every commit.
