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
2. Ensure local hooks are enabled (required): `git config core.hooksPath .githooks`
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

# Enable hardening hooks (required)
./scripts/setup_hooks.sh

# Create config
cp config.example.json config.json
# Edit config.json with your bot token and user ID

# Build
go build -o nasbot ./...

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
- Use `config.example.json` for templates and examples.
- Follow [SECURITY.md](SECURITY.md) before release/tag.

## Project Structure

```
.
├── internal/
│   ├── app/             # Core application logic, handlers, monitors
│   ├── cmdexec/         # Command execution wrapper
│   ├── format/          # Formatting utilities
│   └── model/           # Internal models
├── pkg/
│   ├── commands/        # Command registration and parsing
│   ├── config/          # Configuration loading and types
│   └── model/           # Public models and types
├── scripts/             # Tooling and operational scripts
├── docs/                # Documentation and governance
├── .githooks/           # Local commit hooks (secret scanner)
├── config.example.json  # Example config for new users
├── config.json          # Your config (gitignored)
├── go.mod               # Go module definition
├── go.sum               # Dependency checksums
├── README.md            # Documentation
└── LICENSE              # MIT License
```

## Adding New Commands

1. Register the command in `pkg/commands/registry.go`
2. Create a handler function `handleXxx()` or `getXxxText()` in `internal/app/`
3. Bind the handler in `internal/app/commands_runtime_bindings.go`
4. Update README.md and `BOTFATHER_COMMANDS.txt` with the new command

## Questions?

Feel free to open an issue with the "question" label.

Thank you for contributing! 🙏
