---
name: Agent task
about: A scoped task suitable for execution by an AI agent (or a human who appreciates structure)
title: ""
labels: ["agent-task"]
---

<!--
Use this template when filing work that should be picked up by an AI
agent or by a contributor who wants a fully scoped task. Vagueness is
the enemy: every section below should be filled in. If you cannot fill
in "Files allowed to touch" and "Files off-limits", the task isn't ready
to assign yet.
-->

## Context

<!-- What is the situation? Why are we doing this now? Link any prior
issues, PRs, ADRs, or chat threads. Two or three sentences. -->

## Goal

<!-- What does success look like, in concrete terms? One paragraph,
imperative voice. Avoid vague verbs like "improve" or "tidy up". -->

## Files allowed to touch

<!-- List paths or globs. Be specific. If a path is in scope conditionally,
say so (e.g. "internal/recorder/*.go — only the segment-rotation path"). -->

- 

## Files off-limits

<!-- Files or globs the agent must NOT modify, even if it looks
tempting. Common entries: media-pipeline packages, dependencies, public
API shapes without a paired doc update, unrelated tests. -->

- 

## Acceptance criteria

<!-- A checklist of verifiable conditions. Each item should be testable
or visibly true on inspection. -->

- [ ] 
- [ ] 
- [ ] `make test-nodocker` passes
- [ ] `make lint` passes
- [ ] No unrelated edits in the diff

## Relevant ADRs and canonical entities

<!-- Which ADRs and which canonical entities (from
../../docs/domain-model.md) does this task interact with? If the task
touches auth/pairing/trust, list the parent docs that may need updating
in the same PR. -->

- ADRs:
- Canonical entities:
- Parent docs likely to need an update:

## Notes / non-goals

<!-- Anything explicitly NOT in scope, useful background context, or
known gotchas the agent should be aware of. -->
