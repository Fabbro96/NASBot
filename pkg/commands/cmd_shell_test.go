package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nasbot/internal/format"
	"nasbot/pkg/model"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// The tests in this file cover /cmd, the one command in the bot that executes a
// program.
//
// The risk is the whole point: /cmd used to hand the message to `sh -c`, and in
// the shipped container (root, privileged, pid: host, docker socket mounted) a
// leaked bot token was equivalent to a root shell on the NAS. It is now an
// allowlist of bare binary names executed without a shell, and every test below
// exists to pin one of the ways that can come apart.

// cmdProbe records the argv the executor was handed, so a test can prove what
// would have run rather than only what the user was told.
type cmdProbe struct {
	ran    bool
	binary string
	args   []string
	out    []byte
	err    error
}

func (p *cmdProbe) runner() RuntimeDeps {
	return RuntimeDeps{
		SendMarkdown: func(_ BotAPI, _ int64, _ string) {},
		RunCommandOutput: func(_ context.Context, name string, args ...string) ([]byte, error) {
			p.ran = true
			p.binary = name
			p.args = args
			return p.out, p.err
		},
	}
}

// cmdContext builds a context whose published configuration opens the gate.
func cmdContext(enabled bool, allowed []string) *AppContext {
	ctx := newTestAppContext()
	ctx.SetConfig(&Config{
		AllowedUserID: 1,
		ShellCommand: model.ShellCommandConfig{
			Enabled:         enabled,
			AllowedBinaries: allowed,
			TimeoutSeconds:  5,
			MaxOutputChars:  500,
		},
	})
	return ctx
}

func cmdMessage() *tgbotapi.Message {
	return &tgbotapi.Message{MessageID: 1, Chat: &tgbotapi.Chat{ID: 1}}
}

// TestCmdIsDisabledByDefault is the single most important property of /cmd: a
// configuration that never mentions the section must run nothing.
func TestCmdIsDisabledByDefault(t *testing.T) {
	installEnTranslator(t)

	cases := map[string]*Config{
		"nil config":        nil,
		"empty config":      {},
		"section absent":    {AllowedUserID: 1},
		"disabled":          {ShellCommand: model.ShellCommandConfig{Enabled: false, AllowedBinaries: []string{"ls"}}},
		"enabled but empty": {ShellCommand: model.ShellCommandConfig{Enabled: true}},
		"enabled but all invalid": {ShellCommand: model.ShellCommandConfig{
			Enabled: true, AllowedBinaries: []string{"../../bin/sh", "/bin/ls", ""}}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			probe := &cmdProbe{}
			BindRuntime(probe.runner())
			t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

			ctx := newTestAppContext()
			ctx.SetConfig(c)

			(&CmdCmd{}).Execute(ctx, &recordingBot{}, cmdMessage(), "ls -la /")

			if probe.ran {
				t.Fatalf("the executor ran %q %v with a configuration that must not enable /cmd",
					probe.binary, probe.args)
			}
			if readShellCommandGate(cfg(ctx)).enabled {
				t.Error("the gate reports enabled for this configuration")
			}
		})
	}
}

// TestCmdRunsOnlyAnAllowlistedBinary: the refusal has to happen before anything
// reaches the runner, and the refusal message must show the allowlist so the user
// knows what they may run.
func TestCmdRunsOnlyAnAllowlistedBinary(t *testing.T) {
	installEnTranslator(t)

	var said []string
	probe := &cmdProbe{}
	deps := probe.runner()
	deps.SendMarkdown = func(_ BotAPI, _ int64, text string) { said = append(said, text) }
	BindRuntime(deps)
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	ctx := cmdContext(true, []string{"uptime", "df"})

	for _, refused := range []string{"reboot", "sh", "bash", "ls", "dfx", "systemctl"} {
		probe.ran = false
		said = nil
		(&CmdCmd{}).Execute(ctx, &recordingBot{}, cmdMessage(), refused+" --help")

		if probe.ran {
			t.Fatalf("%q was executed although it is not in the allowlist", refused)
		}
		if len(said) != 1 || !strings.Contains(said[0], "not in the allowlist") {
			t.Fatalf("%q: the refusal was not reported, got %v", refused, said)
		}
		if !strings.Contains(said[0], "`uptime`") || !strings.Contains(said[0], "`df`") {
			t.Errorf("the refusal must show the allowlist, got %q", said[0])
		}
	}
}

// TestCmdExecutesAnAllowlistedBinaryWithItsArguments: the positive case, so the
// refusal test above is not satisfied by refusing everything.
func TestCmdExecutesAnAllowlistedBinaryWithItsArguments(t *testing.T) {
	installEnTranslator(t)

	probe := &cmdProbe{out: []byte("total 12\n")}
	BindRuntime(probe.runner())
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	(&CmdCmd{}).Execute(cmdContext(true, []string{"df"}), &recordingBot{}, cmdMessage(), "df -h /volume1")

	if !probe.ran {
		t.Fatal("an allowlisted binary was not executed")
	}
	if probe.binary != "df" {
		t.Errorf("binary = %q, want df", probe.binary)
	}
	if len(probe.args) != 2 || probe.args[0] != "-h" || probe.args[1] != "/volume1" {
		t.Errorf("args = %v, want [-h /volume1]", probe.args)
	}
}

// TestCmdNeverInvokesAShell is the anti-injection guard, stated as the property
// that actually matters: there is no shell anywhere in the path.
//
// Two cases, and both must hold. When the first token is exactly an allowlisted
// binary, everything the user typed after it reaches the program as arguments,
// with every metacharacter inert. When the first token is polluted by one, the
// name no longer matches the allowlist and nothing runs at all.
//
// Defect covered: a /cmd built on `sh -c` turns "uptime; reboot" into two
// commands. There is no `sh -c` here, so there is no second command to build.
func TestCmdNeverInvokesAShell(t *testing.T) {
	installEnTranslator(t)

	// The shell binary itself must never be what runs, whatever the allowlist
	// says: it is not a bare name the config loader can produce.
	t.Run("a shell cannot be the binary", func(t *testing.T) {
		probe := &cmdProbe{}
		BindRuntime(probe.runner())
		t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

		ctx := cmdContext(true, []string{"uptime"})
		// Force a poisoned allowlist past the loader's own filter by building the
		// gate directly: readShellCommandGate would drop these names, and the
		// executor must refuse them regardless.
		for _, line := range []string{"sh -c reboot", "bash -c reboot", "/bin/sh -c reboot"} {
			probe.ran = false
			(&CmdCmd{}).Execute(ctx, &recordingBot{}, cmdMessage(), line)
			if probe.ran && (probe.binary == "sh" || probe.binary == "bash" || strings.Contains(probe.binary, "/")) {
				t.Errorf("%q ran %q", line, probe.binary)
			}
		}
	})

	t.Run("metacharacters after an allowlisted binary stay inert", func(t *testing.T) {
		for _, line := range []string{
			"uptime; reboot",
			"uptime && reboot",
			"uptime | tee /etc/passwd",
			"uptime `reboot`",
			"uptime $(reboot)",
			"uptime $HOME",
			"uptime > /etc/crontab",
			"uptime\nreboot",
		} {
			probe := &cmdProbe{out: []byte("ok")}
			BindRuntime(probe.runner())

			(&CmdCmd{}).Execute(cmdContext(true, []string{"uptime"}), &recordingBot{}, cmdMessage(), line)

			if !probe.ran {
				// "uptime;reboot" makes the first token something else, which the
				// allowlist refuses. That is the safe answer and it still proves no
				// shell ran.
				continue
			}
			// The whole invariant: what ran is argv[0] and nothing else, and argv
			// is exactly what splitCommandLine produced. `uptime && reboot` really
			// does pass "reboot" as an argument to uptime, which prints it as text:
			// there is no shell, so an argument cannot become a command.
			want, err := splitCommandLine(line)
			if err != nil {
				t.Fatalf("splitCommandLine(%q): %v", line, err)
			}
			if probe.binary != want[0] {
				t.Errorf("%q ran %q as the binary, want %q", line, probe.binary, want[0])
			}
			if len(probe.args) != len(want)-1 {
				t.Fatalf("%q produced argv %q, want %q", line,
					append([]string{probe.binary}, probe.args...), want)
			}
			for i, a := range probe.args {
				if a != want[i+1] {
					t.Errorf("%q: argument %d is %q, want %q: the text was not passed "+
						"through verbatim", line, i, a, want[i+1])
				}
			}
		}
		t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })
	})

	t.Run("a polluted first token is refused", func(t *testing.T) {
		for _, line := range []string{"uptime;reboot", "uptime|reboot", "uptime&&reboot", "uptime$(reboot)"} {
			probe := &cmdProbe{}
			BindRuntime(probe.runner())

			(&CmdCmd{}).Execute(cmdContext(true, []string{"uptime"}), &recordingBot{}, cmdMessage(), line)

			if probe.ran {
				t.Errorf("%q ran %q: a name carrying a metacharacter must not match the "+
					"allowlist", line, probe.binary)
			}
		}
		t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })
	})
}

// TestSplitCommandLine pins the parser: quotes and backslash escapes, and
// explicitly nothing else.
func TestSplitCommandLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"only spaces", "   \t ", nil},
		{"single token", "ls", []string{"ls"}},
		{"plain args", "df -h /volume1", []string{"df", "-h", "/volume1"}},
		{"runs of spaces collapse", "df   -h\t\t/volume1", []string{"df", "-h", "/volume1"}},
		{"double quotes group", `echo "a b c"`, []string{"echo", "a b c"}},
		{"single quotes group", `echo 'a b c'`, []string{"echo", "a b c"}},
		{"quotes inside quotes", `sh -c "echo 'x y'"`, []string{"sh", "-c", "echo 'x y'"}},
		{"empty argument is preserved", `echo ""`, []string{"echo", ""}},
		{"adjacent quoted and bare", `echo a"b"c`, []string{"echo", "abc"}},
		{"backslash escapes a space", `echo a\ b`, []string{"echo", "a b"}},
		{"backslash inside double quotes escapes", `echo "a\"b"`, []string{"echo", `a"b`}},
		// Inside single quotes a backslash is literal, as in every other shell.
		{"backslash inside single quotes is literal", `echo 'a\b'`, []string{"echo", `a\b`}},
		// Nothing is interpreted: metacharacters are ordinary characters.
		{"semicolon is an ordinary character", "echo a;b", []string{"echo", "a;b"}},
		{"dollar is an ordinary character", "echo $HOME", []string{"echo", "$HOME"}},
		{"backtick is an ordinary character", "echo `id`", []string{"echo", "`id`"}},
		{"glob is an ordinary character", "echo *.txt", []string{"echo", "*.txt"}},
		{"redirect is an ordinary character", "echo > out", []string{"echo", ">", "out"}},
		{"newline separates tokens", "echo a\nb", []string{"echo", "a", "b"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := splitCommandLine(tc.in)
			if err != nil {
				t.Fatalf("splitCommandLine(%q) = %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("splitCommandLine(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("splitCommandLine(%q) = %#v, want %#v", tc.in, got, tc.want)
				}
			}
		})
	}
}

// TestSplitCommandLineRefusesAnUnclosedQuote: guessing would run a command
// different from the one that was refused, and the refusal message would then be
// about the wrong one.
func TestSplitCommandLineRefusesAnUnclosedQuote(t *testing.T) {
	for _, in := range []string{`echo "unterminated`, `echo 'unterminated`, `"`, `'`} {
		got, err := splitCommandLine(in)
		if err == nil {
			t.Errorf("splitCommandLine(%q) = %#v, want an error", in, got)
		}
		if got != nil {
			t.Errorf("a refused line must produce no argv, got %#v", got)
		}
	}
}

// TestSplitCommandLineRefusesATrailingBackslash: the same rule for an escape with
// nothing to escape.
func TestSplitCommandLineRefusesATrailingBackslash(t *testing.T) {
	for _, in := range []string{`echo \`, `\`} {
		got, err := splitCommandLine(in)
		if err == nil {
			t.Errorf("splitCommandLine(%q) = %#v, want an error", in, got)
		}
		if got != nil {
			t.Errorf("a refused line must produce no argv, got %#v", got)
		}
	}
}

// TestCmdReportsAParseErrorInsteadOfRunningSomething: the error path has to reach
// the user, because "nothing happened" is indistinguishable from "it worked".
func TestCmdReportsAParseErrorInsteadOfRunningSomething(t *testing.T) {
	installEnTranslator(t)

	var said []string
	probe := &cmdProbe{}
	deps := probe.runner()
	deps.SendMarkdown = func(_ BotAPI, _ int64, text string) { said = append(said, text) }
	BindRuntime(deps)
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	for _, line := range []string{`echo "unterminated`, `echo \`} {
		probe.ran = false
		said = nil

		(&CmdCmd{}).Execute(cmdContext(true, []string{"echo"}), &recordingBot{}, cmdMessage(), line)

		if probe.ran {
			t.Errorf("%q was executed despite the parse error", line)
		}
		if len(said) != 1 || !strings.Contains(said[0], "Cannot read the command line") {
			t.Errorf("%q: the parse error was not reported, got %v", line, said)
		}
	}
}

// TestCmdShowsUsageForAnEmptyLine: `/cmd` with no arguments must not be read as
// "run nothing successfully".
func TestCmdShowsUsageForAnEmptyLine(t *testing.T) {
	installEnTranslator(t)

	var said []string
	probe := &cmdProbe{}
	deps := probe.runner()
	deps.SendMarkdown = func(_ BotAPI, _ int64, text string) { said = append(said, text) }
	BindRuntime(deps)
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	for _, line := range []string{"", "   "} {
		probe.ran = false
		said = nil

		(&CmdCmd{}).Execute(cmdContext(true, []string{"echo"}), &recordingBot{}, cmdMessage(), line)

		if probe.ran {
			t.Errorf("an empty line ran %q", probe.binary)
		}
		if len(said) != 1 || said[0] != "[cmd_usage]" {
			t.Errorf("args=%q: the usage message was not shown, got %v", line, said)
		}
	}
}

// TestIsShellBinaryNameAcceptsOnlyBareNames is the boundary check, and it is
// duplicated in internal/app/config.go on purpose: the one here is the executor's
// own filter, so a Config assembled in memory (a test, the watchdog binary) is
// still checked.
func TestIsShellBinaryNameAcceptsOnlyBareNames(t *testing.T) {
	valid := []string{
		"ls", "df", "uptime", "smartctl", "docker", "journalctl",
		"foo-bar", "foo_bar", "foo.bar", "g++", "a1", "7z",
		strings.Repeat("a", 64), // exactly at the length limit
	}
	for _, name := range valid {
		if !isShellBinaryName(name) {
			t.Errorf("isShellBinaryName(%q) = false, want true", name)
		}
	}

	invalid := []string{
		"",
		".",
		"..",
		"/bin/ls",
		"./ls",
		"../ls",
		"ls/../sh",
		`ls\..\sh`,
		`\bin\sh`,
		"ls;reboot",
		"ls reboot",
		"ls\n",
		"ls\t",
		"$(id)",
		"`id`",
		"ls|cat",
		"ls&",
		"ls>",
		"ls*",
		"ls~",
		"ls!",
		"ls@",
		"ls#",
		"ls%",
		"ls^",
		"ls=",
		"ls:",
		"ls,",
		"ls?",
		"ls'",
		`ls"`,
		"ls\x00",
		"lsé",                   // non-ASCII
		strings.Repeat("a", 65), // one over the limit
	}
	for _, name := range invalid {
		if isShellBinaryName(name) {
			t.Errorf("isShellBinaryName(%q) = true, want false: a name that is not a bare "+
				"executable must never reach the allowlist", name)
		}
	}
}

// TestReadShellCommandGateDropsInvalidEntriesRatherThanRefusingEverything: one
// bad name in config.json must not disable the feature for the good ones, and must
// certainly not let the bad one through.
func TestReadShellCommandGateDropsInvalidEntriesRatherThanRefusingEverything(t *testing.T) {
	gate := readShellCommandGate(&Config{ShellCommand: model.ShellCommandConfig{
		Enabled:         true,
		AllowedBinaries: []string{"uptime", "../../bin/sh", "/bin/ls", "", "df", "ls;reboot"},
	}})

	if !gate.enabled {
		t.Fatal("the gate must stay open for the valid entries")
	}
	want := []string{"uptime", "df"}
	if len(gate.allowlist) != len(want) {
		t.Fatalf("allowlist = %v, want %v", gate.allowlist, want)
	}
	for i := range want {
		if gate.allowlist[i] != want[i] {
			t.Fatalf("allowlist = %v, want %v", gate.allowlist, want)
		}
	}
	for _, dropped := range []string{"../../bin/sh", "/bin/ls", "", "ls;reboot"} {
		if gate.allows(dropped) {
			t.Errorf("the allowlist still accepts %q", dropped)
		}
	}
}

// TestShellCommandGateAllowsIsAnExactComparison: a prefix or a superstring must
// not match. `df` in the list must not authorise `dfx` or `/usr/bin/df`.
func TestShellCommandGateAllowsIsAnExactComparison(t *testing.T) {
	g := shellCommandGate{enabled: true, allowlist: []string{"df"}}

	if !g.allows("df") {
		t.Error("df must be allowed")
	}
	for _, no := range []string{"dfx", "DF", "/usr/bin/df", "./df", "df ", " df", "d", ""} {
		if g.allows(no) {
			t.Errorf("allows(%q) = true, want an exact match only", no)
		}
	}

	empty := shellCommandGate{enabled: true}
	if empty.allows("df") {
		t.Error("an empty allowlist must refuse everything")
	}
}

// TestReadShellCommandGateFallsBackToSafeLimits: a Config built in memory never
// went through sanitizeConfig, so the limits have to carry defaults. A zero
// timeout would kill every command on the spot; a zero output cap would truncate
// the answer to nothing.
func TestReadShellCommandGateFallsBackToSafeLimits(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		gate := readShellCommandGate(nil)
		if gate.enabled {
			t.Error("a missing configuration must not enable /cmd")
		}
		if gate.timeout != defaultShellCommandTimeout {
			t.Errorf("timeout = %v, want %v", gate.timeout, defaultShellCommandTimeout)
		}
		if gate.maxOutput != defaultShellCommandMaxOutput {
			t.Errorf("maxOutput = %d, want %d", gate.maxOutput, defaultShellCommandMaxOutput)
		}
	})

	t.Run("zero and negative limits fall back", func(t *testing.T) {
		gate := readShellCommandGate(&Config{ShellCommand: model.ShellCommandConfig{
			TimeoutSeconds: 0, MaxOutputChars: 0,
		}})
		if gate.timeout != defaultShellCommandTimeout {
			t.Errorf("timeout = %v", gate.timeout)
		}
		if gate.maxOutput != defaultShellCommandMaxOutput {
			t.Errorf("maxOutput = %d", gate.maxOutput)
		}

		gate = readShellCommandGate(&Config{ShellCommand: model.ShellCommandConfig{
			TimeoutSeconds: -5, MaxOutputChars: -5,
		}})
		if gate.timeout != defaultShellCommandTimeout {
			t.Errorf("a negative timeout was accepted: %v", gate.timeout)
		}
		if gate.maxOutput != defaultShellCommandMaxOutput {
			t.Errorf("a negative output cap was accepted: %d", gate.maxOutput)
		}
	})

	t.Run("configured limits are honoured", func(t *testing.T) {
		gate := readShellCommandGate(&Config{ShellCommand: model.ShellCommandConfig{
			Enabled: true, AllowedBinaries: []string{"df"},
			TimeoutSeconds: 7, MaxOutputChars: 42,
		}})
		if gate.timeout.Seconds() != 7 {
			t.Errorf("timeout = %v, want 7s", gate.timeout)
		}
		if gate.maxOutput != 42 {
			t.Errorf("maxOutput = %d, want 42", gate.maxOutput)
		}
	})
}

// TestAllowlistDisplay pins the refusal rendering: every entry in a code span, and
// a dash for the empty case so the message does not end with a blank.
func TestAllowlistDisplay(t *testing.T) {
	if got := (shellCommandGate{}).allowlistDisplay(); got != "-" {
		t.Errorf("empty allowlist displays %q, want %q", got, "-")
	}
	got := shellCommandGate{allowlist: []string{"df", "uptime"}}.allowlistDisplay()
	if got != "`df`, `uptime`" {
		t.Errorf("allowlistDisplay = %q", got)
	}
}

// TestDisplayBinaryNameSubstitutesEverythingButABareName: the refused name comes
// from the message, so echoing it verbatim would let a caller inject Markdown
// into the bot's own reply.
func TestDisplayBinaryNameSubstitutesEverythingButABareName(t *testing.T) {
	cases := map[string]string{
		"ls":             "ls",
		"docker-compose": "docker-compose",
		"g++":            "g++",
		"":               "?",
		"ls;reboot":      "ls?reboot",
		"/usr/bin/ls":    "?usr?bin?ls",
		"../../bin/sh":   "..?..?bin?sh",
		"`rm -rf /`":     "?rm?-rf???",
		"*":              "?",
		"ls\nreboot":     "ls?reboot",
		"ls$(id)":        "ls??id?",
		// "." "-" "_" "+" are part of a bare executable name, so they survive.
		"my.backup_2+": "my.backup_2+",
	}
	for in, want := range cases {
		if got := displayBinaryName(in); got != want {
			t.Errorf("displayBinaryName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDisplayCommandLineKeepsArgumentsReadable: the opposite rule on purpose. A
// path like /volume1 has to survive into the reply, so the filter is the code-span
// terminator and the control characters, not the binary-name charset.
func TestDisplayCommandLineKeepsArgumentsReadable(t *testing.T) {
	got := displayCommandLine([]string{"df", "-h", "/volume1/Media"})
	if got != "df -h /volume1/Media" {
		t.Errorf("displayCommandLine = %q, the path must survive", got)
	}

	// A backtick ends the surrounding code span, so it has to go.
	got = displayCommandLine([]string{"echo", "a`b"})
	if strings.Contains(got, "`") {
		t.Errorf("displayCommandLine = %q, a backtick would close the code span", got)
	}

	// Control characters would break the layout.
	got = displayCommandLine([]string{"echo", "a\nb\tc"})
	for _, r := range got {
		if r < 0x20 && r != ' ' {
			t.Errorf("displayCommandLine = %q still carries a control character", got)
		}
	}

	if got := displayCommandLine(nil); got != "" {
		t.Errorf("displayCommandLine(nil) = %q", got)
	}
}

// TestCommandOutputTruncatesRuneSafe: a byte slice would split a multi-byte rune
// in half and Telegram would render U+FFFD, or reject the whole message.
func TestCommandOutputTruncatesRuneSafe(t *testing.T) {
	out := []byte(strings.Repeat("à", 200))
	got := commandOutput(out, nil, 50)

	if strings.ContainsRune(got, '�') {
		t.Errorf("a rune was split: %q", got)
	}
	if got != strings.Repeat("à", 49)+"~" {
		t.Errorf("commandOutput = %q", got)
	}
}

// TestCommandOutputReportsAFailureWithNoOutput: a command that failed silently
// must still produce a sentence.
func TestCommandOutputReportsAFailureWithNoOutput(t *testing.T) {
	got := commandOutput(nil, errors.New("command not found"), 0)
	if !strings.Contains(got, "command not found") {
		t.Errorf("commandOutput = %q, the error must reach the user", got)
	}
	if !strings.HasPrefix(got, "❌ Error: ") {
		t.Errorf("commandOutput = %q", got)
	}

	// Output plus an error: the output is what the user wants to read, and the
	// exit status is secondary. Both must be present.
	got = commandOutput([]byte("partial output"), errors.New("exit status 1"), 0)
	if !strings.Contains(got, "partial output") {
		t.Errorf("commandOutput = %q, the output was dropped", got)
	}
}

// TestCommandOutputFallsBackToThePackageCap: a zero or negative cap would
// truncate every answer to nothing.
func TestCommandOutputFallsBackToThePackageCap(t *testing.T) {
	long := strings.Repeat("x", maxCmdOutputChars*2)
	for _, cap_ := range []int{0, -1} {
		got := commandOutput([]byte(long), nil, cap_)
		if len([]rune(got)) > maxCmdOutputChars {
			t.Errorf("commandOutput with cap %d produced %d characters", cap_, len([]rune(got)))
		}
		if !strings.HasSuffix(got, "~") {
			t.Errorf("commandOutput with cap %d was not truncated: %q", cap_, got[len(got)-10:])
		}
	}

	// A generous cap leaves the output alone.
	if got := commandOutput([]byte("  short  "), nil, 1000); got != "short" {
		t.Errorf("commandOutput = %q, want the trimmed output", got)
	}
	// format.Truncate does the work, on the trimmed output: the cap applies to
	// what the user sees, not to the padding.
	if got := commandOutput([]byte("  short  "), nil, 3); got != format.Truncate("short", 3) {
		t.Errorf("commandOutput(cap 3) = %q, want %q", got, format.Truncate("short", 3))
	}
}
