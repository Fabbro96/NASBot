package app

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
)

// blockedCommands are the binaries a unit test must never execute, and the canned
// answers every other one gets.
//
// The package binds the real runtime dependencies in an init()
// (commands_runtime_bindings.go), so a test that reaches a handler, a monitor or
// a text generator reaches the host with it: `docker ps`, `dmesg`, `ps`,
// `sudo smartctl`. Those read-only probes make a suite that is green here and
// red on the next NAS (or hangs on a host without the binary). The first group is
// worse than host-dependent, it is destructive:
//
//   - reboot / shutdown / poweroff / halt / init end the session. The OOM loop
//     path (monitor_kernel.go:304) reaches `reboot` after oomLoopThreshold OOM
//     kills, one event away from a test that leaves the runner uninstalled.
//   - systemctl / service drive units, docker.service included.
//   - sudo is how readDiskSMART escalates to smartctl.
//
// `docker ps` is read-only, so it is answered from cannedOutput instead; the
// subcommands that change something are refused by mutatingDockerSubcommands.
//
// So the package-wide runner installed by TestMain executes nothing at all: a
// command the tests did not plan for comes back as an error, and the package
// fails at the end listing everything that was reached. A test that needs
// something else installs its own runner with setCommandRunner.
var (
	// blockedCommands are refused outright and reported.
	blockedCommands = map[string]bool{
		"reboot":    true,
		"shutdown":  true,
		"poweroff":  true,
		"halt":      true,
		"init":      true,
		"systemctl": true,
		"service":   true,
		"sudo":      true,
	}

	// mutatingDockerSubcommands change the host: a test that reaches one of them
	// is reaching the Docker daemon, not a fixture. The read-only ones (ps,
	// inspect, images, version, info) stay in cannedOutput.
	mutatingDockerSubcommands = map[string]bool{
		"restart": true, "stop": true, "kill": true, "rm": true, "rmi": true,
		"prune": true, "start": true, "create": true, "exec": true, "pull": true,
		"push": true, "update": true, "cp": true, "commit": true, "build": true,
		"compose": true, "system": true, "network": true, "volume": true,
		"save": true, "load": true, "tag": true, "rename": true, "port": true,
	}

	// cannedOutput answers the read-only probes the suite legitimately needs.
	cannedOutput = map[string]string{
		// `ps -Ao pid,comm,pcpu,pmem --sort=-pcpu`, one row per line.
		"ps": "  PID COMMAND         %CPU %MEM\n    1 systemd           0.0  0.1\n" +
			"  424 nasbot            1.2  3.4\n  901 dockerd           0.5  1.1\n",
		// `docker ps -a --format {{json .}}` is answered by dockerProbeRunner in
		// the Docker tests; this keeps the shape available everywhere else.
		"docker": `{"Names":"fixture","Status":"Up 2 hours","State":"running","Image":"img","ID":"deadbeef"}`,
		// dmesg / journalctl carry no kernel alerts and no boot history.
		"dmesg":      "",
		"journalctl": "",
		"smartctl": "ID# ATTRIBUTE_NAME          FLAG     VALUE WORST THRESH TYPE      UPDATED  WHEN_FAILED RAW_VALUE\n" +
			"  5 Temperature_Celsius     0x0032   35   40    0    -12.2 Celsius       -        -         35\n" +
			"  9 Power_On_Hours         0x0032   98   98    -     -    -            -        -         35127\n",
		// An empty `ls` / `df` keeps the disk inventory stable.
		"ls":   "",
		"df":   "",
		"du":   "",
		"find": "",
		// `systemctl is-active docker` (blocked above, kept for the record).
		"swapon": "",
		"free":   "",
	}
)

// commandRecorder is the runner installed for the whole package. It executes
// nothing: blocked commands are refused and reported, everything else is answered
// from cannedOutput.
type commandRecorder struct {
	mu        sync.Mutex
	refused   []string
	attempted map[string]int
}

func newCommandRecorder() *commandRecorder {
	return &commandRecorder{attempted: map[string]int{}}
}

func (w *commandRecorder) note(entry string) {
	w.mu.Lock()
	w.attempted[entry]++
	w.mu.Unlock()
}

func (w *commandRecorder) refuse(name string, args []string) error {
	entry := joinCommand(name, args)
	w.mu.Lock()
	w.refused = append(w.refused, entry)
	w.mu.Unlock()
	return fmt.Errorf("test tripwire: refused to execute %q", entry)
}

// isBlocked reports whether this invocation would change the host.
func isBlocked(name string, args []string) bool {
	base := filepathBase(name)
	if blockedCommands[base] {
		return true
	}
	if base == "docker" && len(args) > 0 && mutatingDockerSubcommands[args[0]] {
		return true
	}
	return false
}

func (w *commandRecorder) answer(name string, args []string) ([]byte, bool) {
	base := filepathBase(name)
	w.note(base)
	if isBlocked(name, args) {
		return nil, false
	}
	out, ok := cannedOutput[base]
	return []byte(out), ok
}

func (w *commandRecorder) Exists(name string) bool {
	base := filepathBase(name)
	w.note(base)
	if blockedCommands[base] {
		return false
	}
	_, ok := cannedOutput[base]
	return ok
}

func (w *commandRecorder) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	out, ok := w.answer(name, args)
	if !ok {
		return nil, w.refuse(name, args)
	}
	return out, nil
}

func (w *commandRecorder) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	out, ok := w.answer(name, args)
	if !ok {
		return nil, w.refuse(name, args)
	}
	return out, nil
}

func (w *commandRecorder) Run(_ context.Context, name string, args ...string) error {
	if _, ok := w.answer(name, args); !ok {
		return w.refuse(name, args)
	}
	return nil
}

// refusals returns the distinct blocked commands that were reached.
func (w *commandRecorder) refusals() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.refused))
	for _, e := range w.refused {
		if !contains(out, e) {
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

// suiteRunner is the package-wide recorder, so a test can inspect what the suite
// reached without replacing the runner.
var suiteRunner = newCommandRecorder()

// TestMain installs the tripwire around the whole suite.
//
// Without it, a test that forgets to install a fake runner runs the real binary:
// the OOM path in TestProcessKernelLinesNoDeadlockOnOM did exactly that, one
// event short of invoking /usr/bin/reboot on the machine running the suite. The
// tripwire turns "a test reached a privileged command" from an accident into a
// reported failure.
func TestMain(m *testing.M) {
	restore := setCommandRunner(suiteRunner)
	code := m.Run()
	restore()

	if refusals := suiteRunner.refusals(); len(refusals) > 0 {
		fmt.Fprintf(os.Stderr,
			"\nthe test suite reached %d privileged command(s); none was executed:\n  %s\n"+
				"Install a fake runner with setCommandRunner in the offending test.\n",
			len(refusals), strings.Join(refusals, "\n  "))
		if code == 0 {
			code = 1
		}
	}
	removeSuiteArtifacts()
	os.Exit(code)
}

// removeSuiteArtifacts deletes the var/ directory the package init created in the
// source tree.
//
// runtime_main.go's init() calls setupLogger, which resolves the default log path
// to var/nasbot.log relative to the working directory. Go runs a package's tests
// with the working directory set to the package source directory, and init() runs
// before TestMain, so no test can prevent the file: every `go test ./internal/app`
// writes internal/app/var/nasbot.log and leaves it behind. The cleanup belongs here
// so the tree is left as it was found; the underlying init is a production file and
// is reported instead of changed.
func removeSuiteArtifacts() {
	const dir = "var"

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			// Never recurse: something else put a directory here and it is not
			// ours to delete.
			return
		}
		_ = os.Remove(dir + string(os.PathSeparator) + e.Name())
	}
	_ = os.Remove(dir) // only succeeds when the directory is now empty
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func filepathBase(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func joinCommand(name string, args []string) string {
	if len(args) == 0 {
		return filepathBase(name)
	}
	return filepathBase(name) + " " + strings.Join(args, " ")
}
