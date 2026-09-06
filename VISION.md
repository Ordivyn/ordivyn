# Ordivyn Vision

Ordivyn is a local-first, deterministic, node-based workflow automation
engine, written in Go.

## Why Ordivyn Exists

Automating a software-engineering task usually means chaining a few steps
together — run a command, call an agent, check a condition, run another
command — and trusting that the chain behaves the same way every time you run
it. Most tools that let you describe such a chain don't actually keep that
trust as they grow: execution order can depend on incidental scheduling
behavior, a failure in one branch can silently take down unrelated work, and
the file that was meant to be a declarative description of the chain slowly
grows a scripting language of its own as new conditions get added.

Ordivyn exists to make repeatability a property of the engine, not a hope
about the workflow author's discipline. Given the same graph of steps and the
same inputs, it schedules and resolves that graph the same way every time —
so a workflow can be retried, diffed against a previous run, and built upon
by another workflow, because the one thing it never does is move under you.

## Where We Are Today

Ordivyn is pre-implementation. Nothing is built yet; this document describes
the direction the first implementation will follow, not functionality that
already exists.

## What We Are Building

The goal is a small Go engine that runs a declared graph of steps — call them
nodes — deterministically: validating the graph before anything executes,
running independent nodes concurrently up to an explicit limit, and reporting
exactly what ran, what failed, and what was skipped as a result.

Its eventual operating model is:

```text
 workflow.yaml ──▶ Schema (compile) ──▶ Engine (validate + schedule)
                                               │
                           ┌───────────────────┼───────────────────┐
                           ▼                   ▼                   ▼
                        Node A              Node B              Node C
                     (independent)     (depends on A)      (depends on A)
                           │                   │                   │
                           └───────────────────┴───────────────────┘
                                               ▼
                                      per-node results, honest
                                      success / failure / skipped
```

Ordivyn will own the guarantees around that graph:

- a graph is validated — no cycles, no dangling dependencies — before a
  single node runs;
- a node starts only once every dependency it declared has resolved, never
  before;
- a failure cancels only its own descendants; independent branches always run
  to completion;
- concurrency is bounded by an explicit, caller-set limit, never by
  incidental scheduling; and
- the schema that describes a workflow and the engine that runs it stay two
  separate layers, so a new requirement is never satisfied by quietly growing
  the YAML into a scripting language.

The CLI is the primary way anyone — a person or another agent — defines,
validates, and runs a workflow. A Web UI, if it exists, is a later window
onto runs the engine already produced, never a second place a workflow gets
defined.

## Principles

### Determinism Is a Scheduling Guarantee

Ordivyn guarantees the schedule, not the content: the same nodes run, in the
same order relative to their dependencies, every time. What an individual
node returns is its own business — a step that calls a language model will
produce different prose on every run, and Ordivyn does not pretend otherwise.
Determinism lives at the scheduler's edge, and that boundary is drawn on
purpose, not blurred to make a bigger promise.

### Engine and Schema Are Separate Layers

The engine has no notion of YAML, files, or syntax; the schema carries no
execution logic. Neither layer is allowed to grow into doing the other's job.
A new capability is never satisfied by default with "add more YAML" — it's
satisfied by asking whether the engine needs new information to govern the
run, or whether a node can already express it.

### Sibling Independence

Parallel work is independent by default. A failure, a cancellation, or a slow
node must never reach across the graph to affect a branch that shares no
dependency with it. Anything that couples independent branches — aborting
siblings on one failure, treating a fan-out as one job that must jointly
succeed — must be an explicit choice the workflow author made, never an
accident of how concurrency was wired up.

### Complexity Must Be Earned

Ordivyn grows brick by brick. The smallest engine that can run one real
workflow end to end comes before graph branching, persistence, or a second
node type — not because those are unimportant, but because a specific,
demonstrated need should drive each one, not anticipation of a need that
might arrive.

### Local First

Ordivyn runs on the user's machine, against a repository the user controls.
It should not require an account, collect provider API keys, or send project
state anywhere the user didn't ask it to go.

### One Binary, No Runtime

Ordivyn ships as a single compiled Go binary. No package manager, no bundled
runtime, no service to operate. Whatever it needs to do, it does with the
standard library first.

## The Intended Experience

1. The user writes a workflow as a short YAML file describing a handful of
   steps and how they depend on each other.
2. Ordivyn validates the graph before running anything and rejects it up
   front if it's malformed.
3. Independent steps run concurrently, up to whatever limit the user asked
   for; dependent steps wait for what they depend on.
4. A failure is contained to its own branch — everything independent of it
   still runs to completion.
5. The user gets back an honest, per-node account of what ran, what failed,
   and what was skipped — not just a single pass/fail for the whole run.

## Direction, Not a Feature Checklist

The path begins with the execution engine — the smallest graph scheduler that
can run one real workflow end to end. From there, Ordivyn can add the YAML
schema, a CLI, richer node types (starting with one concrete kind before any
plugin-style registry), and eventually a Web UI that reads what the engine
already produces.

This direction is deliberately open to change. Each capability should be
introduced as the smallest independently useful step, tested against a real
workflow, and kept only if it earns its complexity.

## What Ordivyn Is Not

Ordivyn is not a hosted service, not a multi-user platform, and not a
general-purpose CI system. It does not evaluate arbitrary expressions inside
its workflow format, and it does not guess at what a workflow author meant
when a graph is ambiguous — it rejects the graph and says why.

The ambition is simple: make a declared sequence of steps behave exactly as
declared, every single time it runs.
