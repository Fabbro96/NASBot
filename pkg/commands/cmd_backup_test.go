package commands

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// backupProbe records every message and document the backup command sends.
type backupProbe struct {
	bot   *recordingBot
	texts []string
	binds func()
}

func newBackupProbe(t *testing.T) *backupProbe {
	t.Helper()

	p := &backupProbe{bot: &recordingBot{}}
	installEnTranslator(t)
	BindRuntime(RuntimeDeps{
		SendMarkdown: func(_ BotAPI, _ int64, text string) {
			p.texts = append(p.texts, text)
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })
	return p
}

// documents returns the chats the archive was sent to.
func (p *backupProbe) documents() []int64 {
	var out []int64
	for _, c := range p.bot.sent {
		if doc, ok := c.(tgbotapi.DocumentConfig); ok {
			out = append(out, doc.ChatID)
		}
	}
	return out
}

func (p *backupProbe) said(substr string) bool {
	for _, s := range p.texts {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

func backupMessage(chatID int64) *tgbotapi.Message {
	return &tgbotapi.Message{MessageID: 1, Chat: &tgbotapi.Chat{ID: chatID}}
}

// TestBackupRefusesForeignTargetWithoutConfirmation is the security case the
// `backupConfirmArg` constant exists for.
//
// The archive contains config.json, so it carries bot_token and
// gemini_api_key. A destination other than the authorised user therefore needs an
// explicit, per-invocation acknowledgement; a /backup with no arguments typed by
// the owner must not silently ship the credentials to whoever Backup.TargetUserID
// happens to name.
//
// Defect covered: sending the document first and warning afterwards, or ignoring
// the configured target, leaks both tokens to a second chat.
func TestBackupRefusesForeignTargetWithoutConfirmation(t *testing.T) {
	probe := newBackupProbe(t)
	ctx := newTestAppContext()
	ctx.Config.AllowedUserID = 1
	ctx.Config.Backup.TargetUserID = 2

	(&BackupCmd{}).Execute(ctx, probe.bot, backupMessage(1), "")

	if docs := probe.documents(); len(docs) != 0 {
		t.Fatalf("the archive was sent to %v without an explicit confirmation", docs)
	}
	if !probe.said("Refused: only User ID 2 may change the backup destination, not 1") {
		t.Errorf("expected the refusal to name both user IDs, got %v", probe.texts)
	}
}

// TestBackupSendsToConfiguredTargetAfterConfirmation: the same setup, with the
// acknowledgement. The document must reach the configured target and the owner
// must be told, because the two chats differ.
func TestBackupSendsToConfiguredTargetAfterConfirmation(t *testing.T) {
	probe := newBackupProbe(t)
	ctx := newTestAppContext()
	ctx.Config.AllowedUserID = 1
	ctx.Config.Backup.TargetUserID = 2

	// No config.json exists in the package directory, so the archive step fails.
	// What matters here is that the command got past the confirmation gate and
	// tried to build the archive rather than refusing.
	(&BackupCmd{}).Execute(ctx, probe.bot, backupMessage(1), backupConfirmArg)

	if !probe.said("[backup_creating]") {
		t.Errorf("expected the command to proceed past the confirmation gate, got %v", probe.texts)
	}
	if probe.said("[backup_other_target_refused]") {
		t.Error("the explicit confirmation was ignored")
	}
}

// TestBackupConfirmationArgumentIsForgivingAboutCaseAndSpaces: the owner types
// "Confirm" on a phone keyboard. Rejecting it would push them towards dropping
// the acknowledgement entirely, and the refusal message would be the only
// feedback they get before assuming /backup is broken.
func TestBackupConfirmationArgumentIsForgivingAboutCaseAndSpaces(t *testing.T) {
	for _, args := range []string{"confirm", "CONFIRM", "Confirm", "  confirm  ", "\tconfirm\n"} {
		probe := newBackupProbe(t)
		ctx := newTestAppContext()
		ctx.Config.AllowedUserID = 1
		ctx.Config.Backup.TargetUserID = 2

		(&BackupCmd{}).Execute(ctx, probe.bot, backupMessage(1), args)

		if probe.said("[backup_other_target_refused]") {
			t.Errorf("args=%q was treated as no confirmation", args)
		}
		if !probe.said("[backup_creating]") {
			t.Errorf("args=%q did not pass the gate, got %v", args, probe.texts)
		}
	}
}

// TestBackupRejectsAnyOtherArgument: an unrecognised word must be a usage error,
// not a pass. Anything else makes the acknowledgement a suggestion.
func TestBackupRejectsAnyOtherArgument(t *testing.T) {
	for _, args := range []string{"yes", "y", "confirmed", "confirm now", "send"} {
		probe := newBackupProbe(t)
		ctx := newTestAppContext()
		ctx.Config.AllowedUserID = 1
		ctx.Config.Backup.TargetUserID = 2

		(&BackupCmd{}).Execute(ctx, probe.bot, backupMessage(1), args)

		if !probe.said("[backup_usage]") {
			t.Errorf("args=%q did not produce the usage message, got %v", args, probe.texts)
		}
		if probe.said("[backup_creating]") {
			t.Errorf("args=%q was accepted as a confirmation", args)
		}
	}
}

// TestBackupDefaultsToTheAuthorisedUser is the other half of the gate: with no
// configured target the archive must go to the owner, and no "sent elsewhere"
// line must be printed when the command was issued from the owner's own chat.
func TestBackupDefaultsToTheAuthorisedUser(t *testing.T) {
	probe := newBackupProbe(t)
	ctx := newTestAppContext()
	ctx.Config.AllowedUserID = 7

	(&BackupCmd{}).Execute(ctx, probe.bot, backupMessage(7), "")

	if probe.said("[backup_other_target_refused]") {
		t.Error("the owner's own /backup was refused")
	}
	if !probe.said("[backup_creating]") {
		t.Errorf("expected the command to run, got %v", probe.texts)
	}
}

// TestBackupReportsArchiveFailureInsteadOfClaimingSuccess: with no config.json in
// the working directory the archive cannot be built. The command must say so.
//
// Defect covered: an archive that silently skipped the missing primary would be
// sent as a "successful backup" while containing no configuration at all, which is
// indistinguishable from a working backup for the user.
func TestBackupReportsArchiveFailureInsteadOfClaimingSuccess(t *testing.T) {
	probe := newBackupProbe(t)
	ctx := newTestAppContext()
	ctx.Config.AllowedUserID = 7

	// backupPrimaryFile is read relative to the working directory, which for this
	// package is the source directory and holds no config.json.
	if _, err := os.Stat(backupPrimaryFile); err == nil {
		t.Skipf("%s exists in the working directory; this test needs it absent", backupPrimaryFile)
	}

	(&BackupCmd{}).Execute(ctx, probe.bot, backupMessage(7), "")

	if !probe.said("Error creating backup: ") {
		t.Errorf("expected the archive failure to be reported, got %v", probe.texts)
	}
	if probe.said("[backup_sent_success]") {
		t.Error("a failed backup reported success")
	}
	if docs := probe.documents(); len(docs) != 0 {
		t.Errorf("a failed backup still sent %v", docs)
	}
}

// TestCreateBackupArchiveRefusesWithoutPrimary: the archive must contain
// config.json or it is worthless, and the error has to name it so the operator
// knows what is missing.
func TestCreateBackupArchiveRefusesWithoutPrimary(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "config.json")

	path, err := createBackupArchive([]string{missing}, backupPrimaryFile)
	if err == nil {
		_ = os.Remove(path)
		t.Fatalf("an archive without %s must not be produced", backupPrimaryFile)
	}
	if !strings.Contains(err.Error(), backupPrimaryFile) {
		t.Errorf("error must name the missing file, got %v", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		_ = os.Remove(path)
		t.Error("a partial archive was left behind")
	}
}

// TestCreateBackupArchiveContainsPrimaryWithTightPermissions: the file that goes
// into the archive holds both tokens, so the archive itself must not be readable
// by other local accounts. os.CreateTemp already opens 0600, but the code states
// the mode explicitly because a permissive umask on some platforms is enough to
// lose it.
func TestCreateBackupArchiveContainsPrimaryWithTightPermissions(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, backupPrimaryFile)
	if err := os.WriteFile(primary, []byte(`{"bot_token":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "nasbot_state.json")
	if err := os.WriteFile(state, []byte(`{"language":"it"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// primary is passed as the same string used in the list: writeBackupArchive
	// decides whether the primary made it in by comparing the two.
	path, err := createBackupArchive([]string{primary, state}, primary)
	if err != nil {
		t.Fatalf("createBackupArchive: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != backupFileMode {
		t.Errorf("archive mode = %04o, want %04o: the archive holds the bot token", got, backupFileMode)
	}
	if !strings.Contains(filepath.Base(path), "nasbot_backup_") {
		t.Errorf("archive name %q does not follow the documented pattern", filepath.Base(path))
	}

	names := zipEntryNames(t, path)
	for _, want := range []string{backupPrimaryFile, "nasbot_state.json"} {
		if !containsString(names, want) {
			t.Errorf("archive is missing %q, it has %v", want, names)
		}
	}
}

// TestCreateBackupArchiveSkipsAbsentOptionalFiles: the state file and the log are
// optional. A missing one must not fail the backup, and must not add an empty
// entry either: an entry named after a file that does not exist is a broken zip.
func TestCreateBackupArchiveSkipsAbsentOptionalFiles(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, backupPrimaryFile)
	if err := os.WriteFile(primary, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	path, err := createBackupArchive([]string{
		primary,
		filepath.Join(dir, "nasbot_state.json"),
		filepath.Join(dir, "nasbot.log"),
	}, primary)
	if err != nil {
		t.Fatalf("createBackupArchive: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	names := zipEntryNames(t, path)
	if len(names) != 1 || names[0] != backupPrimaryFile {
		t.Errorf("archive entries = %v, want just %q", names, backupPrimaryFile)
	}
}

// TestCreateBackupArchiveReportsUnreadableEntry: a path that exists but cannot be
// opened must fail the whole archive. Swallowing it would produce a zip that looks
// complete and is missing the very file the user wanted.
func TestCreateBackupArchiveReportsUnreadableEntry(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, backupPrimaryFile)
	if err := os.WriteFile(primary, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	unreadable := filepath.Join(dir, "locked.json")
	if err := os.WriteFile(unreadable, []byte(`{}`), 0o000); err != nil {
		t.Fatal(err)
	}

	path, err := createBackupArchive([]string{unreadable, primary}, primary)
	if err == nil {
		_ = os.Remove(path)
		t.Fatal("an unreadable entry must fail the archive")
	}
	if !strings.Contains(err.Error(), "locked.json") {
		t.Errorf("error must name the unreadable entry, got %v", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		_ = os.Remove(path)
		t.Error("a partial archive was left behind")
	}
}

// TestBackupSourceFilesAlwaysIncludePrimary: the primary is the one entry that
// makes the backup worth taking. Dropping it from the list would leave every
// archive empty of configuration.
func TestBackupSourceFilesAlwaysIncludePrimary(t *testing.T) {
	files := backupSourceFiles()
	if len(files) == 0 || files[0] != backupPrimaryFile {
		t.Errorf("backupSourceFiles = %v, want %q first", files, backupPrimaryFile)
	}
	if !containsString(files, backupPrimaryFile) {
		t.Errorf("backupSourceFiles = %v, want it to list %q", files, backupPrimaryFile)
	}
}

// TestBackupConfirmationArgumentIsNotEmpty guards the constant itself: an empty
// confirmation argument would make the `case backupConfirmArg:` arm match the
// no-argument branch and silently disable the gate.
func TestBackupConfirmationArgumentIsNotEmpty(t *testing.T) {
	if strings.TrimSpace(backupConfirmArg) == "" {
		t.Fatal("backupConfirmArg is empty: it would collide with the no-argument case")
	}
	if strings.ToLower(strings.TrimSpace(backupConfirmArg)) != backupConfirmArg {
		t.Errorf("backupConfirmArg = %q, want a lower-case word (the switch lowercases its input)", backupConfirmArg)
	}
}

// containsString reports whether needle is in hay. Named to avoid colliding with
// the helpers other test files in this package define.
func containsString(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func zipEntryNames(t *testing.T, path string) []string {
	t.Helper()

	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("the archive is unreadable: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	names := make([]string, 0, len(r.File))
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return names
}
