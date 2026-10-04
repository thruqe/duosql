# AGENTS

If you are an AI reading this, take every single word in this file literally. This project is a focused Go ORM and query builder library designed to unify PostgreSQL and SQLite with maximum performance, builder-centric APIs, and modern Go idioms.

All code here adheres to a deliberate structure. It is built using modern patterns and idioms of the Go programming language (Go 1.23+ and 1.24+). You must conduct thorough research into current Go standards, idiomatic implementations, and active library ecosystems to avoid deprecated functions, superseded APIs, or obsolete syntax.

Do NOT introduce fake enterprise microservice DDD boilerplate (such as `internal/core/ports/adapters`). This is a library. Keep code modular, cohesive, and directly accessible.

Before modifying any file, analyze related modules across the codebase. Observe how functions are implemented, match the prevailing architecture, and understand the rationale behind existing technical decisions. Pay particular attention to comments: for every complex or non-obvious code path, you are expected to understand the author's intent. When submitting bug fixes or feature additions, document your rationale and implementation details to match the tone and conventions established by prior contributors.

You must review the [`Taskfile`](../Taskfile.yml). This is the primary build-and-task automation configuration for this repository; it abstracts repetitive CLI workflows. Always run `task check` before declaring any task complete.

Every function must satisfy:

1. Cyclomatic complexity threshold: <= 15 checked via `task cyclo`.
2. Dead code elimination: verified via `task deadcode`.
3. Modern Go syntax: verified via `task fix`.
4. Concurrency correctness: verified with race detection via `task test`.

Submitting no code is strictly preferable to merging broken, complex, or untested logic.
