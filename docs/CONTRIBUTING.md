# Contributing Guidelines

Welcome to duosql. These guidelines should be reviewed before submitting issues or pull requests.

## 1. Development Management

All development workflows are managed through the [Taskfile](../Taskfile.yml):

| Task       | Command         | Description                                                           |
| :--------- | :-------------- | :-------------------------------------------------------------------- |
| `fmt`      | `task fmt`      | Format and vet all Go files (`go fmt`, `gofmt -s`, `go vet`).         |
| `fix`      | `task fix`      | Apply Go modernizers across all packages.                             |
| `cyclo`    | `task cyclo`    | Verify cyclomatic complexity stays strictly within threshold (<= 15). |
| `deadcode` | `task deadcode` | Analyze unreachable functions across the library and test suite.      |
| `test`     | `task test`     | Run production test suite with race detection enabled.                |
| `check`    | `task check`    | Run all quality gates (`fmt`, `fix`, `cyclo`, `deadcode`, `test`).    |
| `install`  | `task install`  | Download and tidy all Go module dependencies.                         |
| `update`   | `task update`   | Upgrade all Go dependencies across modules.                           |
| `clean`    | `task clean`    | Remove temporary build artifacts and test traces.                     |

## 2. Architecture & Guidelines

- Architecture Guidelines: If you are an AI, please read this file, it is very important [AGENTS](./AGENTS.md)
- Code Quality: `task check` must be run before opening a pull request.
- Cyclomatic Complexity: All functions must maintain cyclomatic complexity <= 15 checked via `gocyclo`.
- Dead Code: Unused private code or abandoned functions are strictly forbidden; verify with `task deadcode`.
- Testing: Production grade tests with `-race` detection must pass using `task test`.
- Commits: Conventional commit conventions must be followed (`feat:`, `fix:`, `refactor:`, `docs:`, `test:`).

## 3. Pull Request Workflow

1. Create a feature branch from `main`.
2. Implement changes following the architecture guidelines.
3. Validate locally with `task check`.
4. Submit a Pull Request targeting the `main` branch.
