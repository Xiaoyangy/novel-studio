# Skills Context Protocol

This directory is the single source for exported novel-studio skills. Every
skill must be readable after conversation compaction without relying on hidden
chat history.

## Required Read Order

When a skill is selected:

1. Read `skills/CONTEXT_PROTOCOL.md` for the shared recovery contract.
2. Read `SKILL.md`.
3. Read `CONTEXT.md` in the same skill directory.
4. Read `context.json` in the same skill directory.
5. Read every path in `required_files`.
6. Read only the `conditional_files` entries that match the current task.
7. If the task is already running, read `.skill-context/<skill>.md` from the
   execution directory before continuing.

The same plan can be inspected with `novel-studio skills context <skill>`,
`novel-studio skills context <skill> --json`, or
`novel-studio skills context --all --json`; this is the preferred quick check
before resuming after compaction or exporting skills into another agent runtime.
When the executing runtime needs a self-contained recovery packet instead of a
path list, run `novel-studio skills context <skill> --content` to materialize
the protocol, entrypoint, manifest, and required files. Add
`--include-conditional` only when the current task genuinely needs the
task-gated references or scripts. Add `--state-dir <execution-dir>` to include
existing files declared in `state_files` and to report declared state files that
are still missing.

## Execution State

Long or multi-stage tasks must create or update `.skill-context/<skill>.md` in
the active project directory. Keep it short and stable:

```markdown
# <skill> execution context
- task: <user request>
- stage: <current phase>
- inputs: <important input paths>
- outputs: <important output paths>
- files_read: <SKILL / CONTEXT / references / scripts already read>
- hard_constraints: <rules that must survive compaction>
- next_step: <first action after resume>
```

Update this file after each stage transition, chapter write, review pass,
script run, handoff to another agent, or before any expected context
compaction.

## Context Budget Rules

- Do not load an entire large `references/` tree when only one workflow branch
  is needed.
- Do not put long正文, full拆文 outputs, platform pages, or script logs into the
  chat as durable state. Save them to files and record the paths in
  `.skill-context/<skill>.md`.
- Main sessions keep only routing, current stage, active paths, constraints and
  next step. Detailed story state belongs in project files.
- If compaction happens and key details are missing, recover from
  `.skill-context/<skill>.md`, `context.json`, and the declared state files
  before continuing.

## Source Ownership

- `skills/` owns skill instructions, routing and exported skill content.
- `quality/audit/` owns local AIGC / AI-tone / duplicate / typo scripts and
  references. Exported `review/scripts` files are assembled from that source.
- `assets/references/` owns runtime references injected by the Go engine.
- Project outputs and task state are not copied into skill directories.
