# Contributing to NASBot

First off, thanks for taking the time to contribute! 🎉

## How Can I Contribute?

### Reporting Bugs

Before creating a bug report, please check existing issues to avoid duplicates.

When creating a bug report, include:
- **Clear title** describing the issue
- **Steps to reproduce** the behavior
- **Expected behavior** vs what actually happened
- **System info**: OS, Go version, Docker version (if applicable)
- **Logs** (run with `./nasbot 2>&1 | tee nasbot.log`)

### Suggesting Features

Feature requests are welcome! Please:
- Check if the feature was already requested
- Describe the use case clearly
- Explain why this would benefit other users

### Pull Requests

1. Fork the repo and create your branch from `main`
2. Ensure local hooks are enabled (required): `./scripts/setup_hooks.sh` (or, manually, `git config core.hooksPath .githooks`)
3. Ensure the code compiles: `go build ./...`
4. Run tests: `go test ./...`
5. If relevant, validate release build: `./scripts/build_release.sh`
6. Update docs if you added new features/commands
7. Submit your PR with a clear description

## Development Setup

```bash
# Clone your fork
git clone https://github.com/YOUR_USERNAME/NASBot.git
cd NASBot

# Install dependencies
go mod download

# Install AND activate the hardening hooks (required)
./scripts/setup_hooks.sh
# setup_hooks.sh writes .githooks/{pre-commit,pre-push,commit-msg} and then runs
# `git config core.hooksPath .githooks` for this clone. Without that last step
# git never runs the files and no guard applies locally.

# Create config
cp config.example.json config.json
chmod 600 config.json   # the bot refuses to start on a group/world-readable config
# Edit config.json with your bot token and user ID

# Build
go build -o nasbot .
# `go build -o nasbot ./...` FAILS with
# "go: cannot write multiple packages to non-directory nasbot":
# the module has one main package plus the library packages, and -o names one file.

# Run
./nasbot
```

## Code Style

- Follow standard Go conventions (`gofmt`)
- Use meaningful variable names
- Comment exported functions and complex logic
- Keep functions focused and reasonably sized

## Security Rules (Required)

- Do not commit real secrets in any file.
- Keep credentials only in local `config.json` (gitignored).
- Keep `config.json` at mode `0600`. NASBot refuses to boot on a config that group
  or others can read, so a copy made without `chmod` is a broken install.
- Use `config.example.json` for templates and examples.
- A new capability that reaches the host (new command, new socket mount, new
  privileged capability) must be written down in [SECURITY.md](../SECURITY.md).
- Follow [SECURITY.md](../SECURITY.md) before release/tag.

## Project Structure

```
.
├── internal/
│   ├── app/             # Core application logic, handlers, monitors, command registry
│   ├── cmdexec/         # Command execution wrapper
│   ├── format/          # Formatting utilities
│   └── model/           # Internal models
├── pkg/
│   ├── commands/        # Command implementations (Execute/Description)
│   └── model/           # Shared models, config types, secret redaction
├── scripts/             # Tooling and operational scripts
├── docs/                # Documentation and governance
├── .githooks/           # Local commit hooks (secret scanner, Conventional Commits)
├── config.example.json  # Example config for new users
├── config.json          # Your config (gitignored, must stay chmod 600)
├── nasbot.config.template / nasbot.config.example
│                        # Shell-level config for the scripts/ (see MODULAR_CONFIG_GUIDE.md)
├── go.mod               # Go module definition
├── go.sum               # Dependency checksums
├── README.md            # Project overview and command reference
├── MODULAR_ARCHITECTURE.md
├── MODULAR_CONFIG_GUIDE.md
└── LICENSE              # MIT License
```

> `pkg/config/` no longer exists. The configuration types live in
> `pkg/model/config_types.go` and loading/sanitizing in `internal/app/config.go`.

## Adding New Commands

1. Register the command in `internal/app/init_commands.go` (`SetupCommandRegistry`).
   That file is the single registry: there is no `pkg/commands/registry.go`.
2. Implement the `Command` interface in `pkg/commands/` (`Execute`, `Description`)
   and add the type alias in `internal/app/commands_aliases.go`.
3. Bind the handler in `internal/app/commands_runtime_bindings.go`.
4. Add the description translation key in all 6 languages
   (`internal/app/translations.go`); `translations_coverage_test.go` enforces coverage.
5. Decide whether the command goes into the Telegram menu that the bot pushes at
   startup (`registerBotCommands` in `internal/app/runtime_main.go`). Commands left
   out of that list must be added to `docs/operations/BOTFATHER_COMMANDS.txt`, or
   the user will not find them in the chat menu.
6. Update `README.md` and the `Unreleased` section of `docs/CHANGELOG.md`.

## Questions?

Feel free to open an issue with the "question" label.

Thank you for contributing! 🙏
