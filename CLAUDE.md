# dcdash — Project Instructions

dcdash is a dashboard for managing central data center services (Proxmox, DNS, DHCP).
It has a Go backend and a Vue 3 + Tailwind CSS frontend, and is licensed under GPLv3.

- Concept: [docs/concept.md](docs/concept.md)
- Decisions: [docs/concept-review.md](docs/concept-review.md)
- Plan and progress: [docs/implementation-plan.md](docs/implementation-plan.md)

## Mandatory workflow

1. **Log every user prompt.** Append each prompt verbatim to
   [docs/zz_prompts.md](docs/zz_prompts.md) under a `## YYYY-MM-DD — Sprint N (<topic>)`
   heading. Commit it together with the work it caused.
2. **One branch per sprint.** Work on `sprint/NN-<topic>`, forked from the current `main`.
   Never commit sprint work directly to `main`.
3. **One or more commits per task.** Start commit messages with the task number, for
   example `3.4: Resolve nested AD groups`.
4. **Track progress in the plan.** Check a task (`[x]`) in
   `docs/implementation-plan.md` in the commit that completes it. Keep the sprint status
   column in the overview table up to date.
5. **Add subtasks for new work.** If a task needs more work than planned, add a subtask
   `<sprint>.<task>.<n>` (for example `1.1.1`) under that task. Never renumber existing
   tasks.
6. **Finish a sprint like this.** Once all tasks are checked, squash-merge the sprint
   branch into `main` with the commit message `Sprint N: <topic>`. Keep the sprint branch
   (never delete it), then fork the next sprint branch from `main`. Merges and pushes
   need the user's confirmation.

## Definition of done

Every task meets the "Definition of done" in the implementation plan:
- English and German UI texts.
- Light and dark mode support.
- Permission checks and audit logging for state changes.
- Tests, and an updated OpenAPI spec.

Logging uses `rs/zerolog`.
