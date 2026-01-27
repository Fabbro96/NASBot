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
2. If you've added code, add comments explaining the logic
3. Ensure the code compiles: `go build -o nasbot .`
4. Test your changes on a real system if possible
5. Update the README if you added new features/commands
6. Submit your PR with a clear description

## Development Setup

```bash
# Clone your fork
git clone https://github.com/YOUR_USERNAME/nasbot.git
cd nasbot

# Install dependencies
go mod download

# Create config
cp config.example.json config.json
# Edit config.json with your bot token and user ID

# Build
go build -o nasbot .

# Run
./nasbot
```

## Code Style

- Follow standard Go conventions (`gofmt`)
- Use meaningful variable names
- Comment exported functions and complex logic
- Keep functions focused and reasonably sized

## Project Structure

```
.
├── main.go              # All bot code (single file for simplicity)
├── config.json          # Your config (gitignored)
├── config.example.json  # Example config for new users
├── go.mod               # Go module definition
├── go.sum               # Dependency checksums
├── README.md            # Documentation
├── LICENSE              # MIT License
└── setup_autostart.sh   # Autostart setup script
```

## Adding New Commands

1. Add the command to `handleCommand()` switch statement
2. Create a function `getXxxText()` for text generation or `handleXxx()` for actions
3. Update `getHelpText()` to document the new command
4. Update README.md with the new command

## Questions?

Feel free to open an issue with the "question" label.

Thank you for contributing! 🙏
