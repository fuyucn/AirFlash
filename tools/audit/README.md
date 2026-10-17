# Bug audit

A checklist-driven audit of the Go shell, run periodically. It is not a linter:
linters report what a rule matches, and the rules that matter here are the ones
that are true of this codebase and not expressible as one.

Each finding is given a severity, the file and line it was seen at, and a
one-sentence statement of what goes wrong. A finding is closed only by a change
that removes it, plus a test that fails without that change.
