package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

const (
	MAX_TELEGRAM_FILE_SIZE = 2000 * 1024 * 1024 // 2000MB in bytes
)

// ProgressReader wraps an io.Reader to track progress
type ProgressReader struct {
	io.Reader
	total      int64
	downloaded int64
	onProgress func(float64)
}

func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.Reader.Read(p)
	pr.downloaded += int64(n)
	if pr.total > 0 {
		progress := float64(pr.downloaded) / float64(pr.total) * 100
		pr.onProgress(progress)
	}
	return n, err
}

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
	}

	bot, err := tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = true
	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		// Check if message starts with /url command
		if strings.HasPrefix(update.Message.Text, "/url ") {
			// Extract URL from the command
			url := strings.TrimPrefix(update.Message.Text, "/url ")
			url = strings.TrimSpace(url)

			if url != "" {
				// Process URL in the same group where command was received
				go handleURL(bot, update.Message, url)
			} else {
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "❌ Please provide a URL after the /url command")
				bot.Send(msg)
			}
		}
	}
}

func handleURL(bot *tgbotapi.BotAPI, message *tgbotapi.Message, url string) {
	// Send initial status message
	statusMsg := tgbotapi.NewMessage(message.Chat.ID, "⏳ Starting download...")
	status, err := bot.Send(statusMsg)
	if err != nil {
		log.Printf("Error sending initial status: %v", err)
		return
	}

	// Get file info before downloading
	resp, err := http.Head(url)
	if err != nil {
		updateMessage(bot, message.Chat.ID, status.MessageID, "❌ Failed to get file info")
		return
	}
	fileSize := resp.ContentLength

	// Check file size before downloading
	// if fileSize > MAX_TELEGRAM_FILE_SIZE {
	// 	sizeMB := float64(fileSize) / 1024 / 1024
	// 	errorMsg := fmt.Sprintf("❌ File is too large (%.1f MB). Telegram bot limit is 50 MB.\n\nPlease use a direct download link instead.", sizeMB)
	// 	updateMessage(bot, message.Chat.ID, status.MessageID, errorMsg)
	// 	return
	// }

	// Start actual download
	resp, err = http.Get(url)
	if err != nil {
		updateMessage(bot, message.Chat.ID, status.MessageID, "❌ Failed to download the file")
		return
	}
	defer resp.Body.Close()

	fileName := filepath.Base(url)
	if fileName == "" {
		fileName = "downloaded_file"
	}

	tempFile, err := os.CreateTemp("", "telegram-*-"+fileName)
	if err != nil {
		updateMessage(bot, message.Chat.ID, status.MessageID, "❌ Failed to create temporary file")
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	lastUpdate := time.Now()
	progressReader := &ProgressReader{
		Reader: resp.Body,
		total:  fileSize,
		onProgress: func(progress float64) {
			// Update status message every 2 seconds to avoid flooding
			if time.Since(lastUpdate) >= 2*time.Second {
				statusText := fmt.Sprintf("⏬ Downloading: %.1f%%", progress)
				updateMessage(bot, message.Chat.ID, status.MessageID, statusText)
				lastUpdate = time.Now()
			}
		},
	}

	_, err = io.Copy(tempFile, progressReader)
	if err != nil {
		updateMessage(bot, message.Chat.ID, status.MessageID, "❌ Failed to save the file")
		return
	}

	// Update status before upload
	updateMessage(bot, message.Chat.ID, status.MessageID, "📤 Uploading to Telegram...")

	// Rewind file for reading
	tempFile.Seek(0, 0)

	// Create the file upload - sending to the same group where command was received
	doc := tgbotapi.NewDocument(message.Chat.ID, tgbotapi.FilePath(tempFile.Name()))
	doc.ReplyToMessageID = message.MessageID

	// Send the file
	_, err = bot.Send(doc)
	if err != nil {
		updateMessage(bot, message.Chat.ID, status.MessageID, "❌ Failed to send the file")
		return
	}

	// Final success message
	updateMessage(bot, message.Chat.ID, status.MessageID, "✅ File sent successfully!")
}

func updateMessage(bot *tgbotapi.BotAPI, chatID int64, messageID int, text string) {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	bot.Send(edit)
}

func sendErrorMessage(bot *tgbotapi.BotAPI, chatID int64, message string) {
	msg := tgbotapi.NewMessage(chatID, message)
	bot.Send(msg)
}
