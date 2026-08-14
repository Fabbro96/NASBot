package commands

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type BackupCmd struct{}

func (c *BackupCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendMarkdown(bot, msg.Chat.ID, ctx.Tr("backup_creating"))

	targetID := ctx.Config.Backup.TargetUserID
	if targetID == 0 {
		targetID = ctx.Config.AllowedUserID
	}

	zipPath := filepath.Join(os.TempDir(), fmt.Sprintf("nasbot_backup_%s.zip", time.Now().Format("20060102_150405")))
	err := createBackupArchive(ctx, zipPath)
	if err != nil {
		sendMarkdown(bot, msg.Chat.ID, fmt.Sprintf(ctx.Tr("backup_create_err"), err))
		return
	}
	defer os.Remove(zipPath) // Clean up after sending

	// Send document
	doc := tgbotapi.NewDocument(targetID, tgbotapi.FilePath(zipPath))
	doc.Caption = ctx.Tr("backup_caption")
	_, err = bot.Send(doc)
	if err != nil {
		sendMarkdown(bot, msg.Chat.ID, fmt.Sprintf(ctx.Tr("backup_send_err"), err))
		return
	}

	if targetID != msg.Chat.ID {
		sendMarkdown(bot, msg.Chat.ID, ctx.Tr("backup_sent_success"))
	}
}

func createBackupArchive(ctx *AppContext, zipPath string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	archive := zip.NewWriter(zipFile)
	defer archive.Close()

	filesToBackup := []string{
		"config.json",
		"var/nasbot_state.json",
		"nasbot_state.json",
		"var/nasbot.log",
		"nasbot.log",
	}

	for _, f := range filesToBackup {
		addFileToZip(archive, f)
	}

	return nil
}

func addFileToZip(archive *zip.Writer, path string) {
	file, err := os.Open(path)
	if err != nil {
		return // File might not exist (e.g. log file), just skip
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return
	}
	header.Name = filepath.Base(path)
	header.Method = zip.Deflate

	writer, err := archive.CreateHeader(header)
	if err != nil {
		return
	}

	_, _ = io.Copy(writer, file)
}

func (c *BackupCmd) Description() string {
	return "Create and send a backup zip of configurations and databases"
}
