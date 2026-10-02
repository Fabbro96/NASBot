package commands

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// backupFileMode: the archive contains config.json (bot_token, gemini_api_key).
// os.Create would apply 0666&~umask; os.CreateTemp already opens with 0600 and
// the explicit Chmod guards against a permissive umask on odd platforms.
const backupFileMode os.FileMode = 0o600

// backupPrimaryFile must end up in the archive: without it the zip is empty of
// configuration and sending it would look like a successful backup.
const backupPrimaryFile = "config.json"

// backupConfirmArg is the explicit acknowledgement required to send the backup
// somewhere else than the authorised user.
const backupConfirmArg = "confirm"

type BackupCmd struct{}

func (c *BackupCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	chatID := msg.Chat.ID
	conf := cfg(ctx)
	if conf == nil {
		sendMarkdown(bot, chatID, trf(ctx.Tr, "backup_create_err", errors.New("configuration unavailable")))
		return
	}

	// Default: the authorised user owns the archive.
	targetID := conf.AllowedUserID
	if conf.Backup.TargetUserID != 0 {
		targetID = conf.Backup.TargetUserID
	}

	switch strings.ToLower(strings.TrimSpace(args)) {
	case "":
		if targetID != conf.AllowedUserID {
			// The archive holds bot_token and gemini_api_key: a destination other
			// than the authorised user needs an explicit, per-invocation ack.
			sendMarkdown(bot, chatID, trf(ctx.Tr, "backup_other_target_refused",
				targetID, conf.AllowedUserID))
			return
		}
	case backupConfirmArg:
		// Explicit acknowledgement of the configured destination, which can only
		// be set by the authorised user through /configset or /settings.
	default:
		sendMarkdown(bot, chatID, ctx.Tr("backup_usage"))
		return
	}

	sendMarkdown(bot, chatID, ctx.Tr("backup_creating"))

	zipPath, err := createBackupArchive(backupSourceFiles(), backupPrimaryFile)
	if err != nil {
		sendMarkdown(bot, chatID, trf(ctx.Tr, "backup_create_err", sanitizeErr(err)))
		return
	}
	defer os.Remove(zipPath) // Clean up after sending

	// Send document
	doc := tgbotapi.NewDocument(targetID, tgbotapi.FilePath(zipPath))
	doc.Caption = ctx.Tr("backup_caption")
	if _, err := bot.Send(doc); err != nil {
		sendMarkdown(bot, chatID, trf(ctx.Tr, "backup_send_err", sanitizeErr(err)))
		return
	}

	if targetID != chatID {
		sendMarkdown(bot, chatID, ctx.Tr("backup_sent_success"))
	}
}

// backupSourceFiles lists the files to archive, primary first.
func backupSourceFiles() []string {
	return []string{
		backupPrimaryFile,
		"var/nasbot_state.json",
		"nasbot_state.json",
		"var/nasbot.log",
		"nasbot.log",
	}
}

// createBackupArchive writes a fresh archive in the temp dir and returns its
// path. On any failure the partial file is removed and the error explains what
// went wrong: the caller must never be handed a truncated zip while telling the
// user the backup succeeded.
//
// The temp name comes from os.CreateTemp (unpredictable, created with
// O_EXCL so it cannot follow a symlink): the previous
// "nasbot_backup_<date>.zip" name was predictable to the second, so two
// /backup in the same second overwrote each other mid-write.
func createBackupArchive(files []string, primary string) (string, error) {
	zipFile, err := os.CreateTemp(os.TempDir(), "nasbot_backup_*.zip")
	if err != nil {
		return "", err
	}
	zipPath := zipFile.Name()
	if err := zipFile.Chmod(backupFileMode); err != nil {
		zipFile.Close()
		os.Remove(zipPath)
		return "", fmt.Errorf("chmod %s: %w", backupFileMode, err)
	}

	if err := writeBackupArchive(zipFile, files, primary); err != nil {
		zipFile.Close()
		os.Remove(zipPath)
		return "", err
	}
	if err := zipFile.Close(); err != nil {
		os.Remove(zipPath)
		return "", fmt.Errorf("close archive file: %w", err)
	}
	return zipPath, nil
}

// writeBackupArchive fills zipFile with files and flushes the zip directory.
// Closing the archive is part of the transaction: without it the central
// directory is missing and the archive is unreadable. primary (config.json)
// must be in the archive, otherwise there is nothing worth sending.
func writeBackupArchive(zipFile *os.File, files []string, primary string) error {
	archive := zip.NewWriter(zipFile)
	primaryAdded := false
	for _, f := range files {
		ok, err := addFileToZip(archive, f)
		if err != nil {
			// Best effort: close so the writer state is consistent, the caller
			// discards the file anyway.
			_ = archive.Close()
			return fmt.Errorf("add %s: %w", f, err)
		}
		if ok && f == primary {
			primaryAdded = true
		}
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("close zip archive: %w", err)
	}
	if !primaryAdded {
		return fmt.Errorf("%s not found or unreadable: archive would be empty", primary)
	}
	return nil
}

// addFileToZip copies path into archive. It reports whether the file was added:
// a missing optional file (log, state) is not an error, every other failure is
// propagated instead of being swallowed.
func addFileToZip(archive *zip.Writer, path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil // optional file, skip
		}
		return false, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return false, err
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return false, err
	}
	header.Name = filepath.Base(path)
	header.Method = zip.Deflate

	writer, err := archive.CreateHeader(header)
	if err != nil {
		return false, err
	}

	if _, err := io.Copy(writer, file); err != nil {
		return false, err
	}
	return true, nil
}

func (c *BackupCmd) Description() string {
	return "Create and send a backup zip of configurations and databases"
}
