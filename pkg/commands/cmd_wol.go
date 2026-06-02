package commands

import (
	"encoding/hex"
	"fmt"
	"net"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type WolCmd struct{}

func (c *WolCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	macStr := strings.TrimSpace(ctx.Config.WakeOnLan.MacAddress)
	if macStr == "" {
		sendMarkdown(bot, msg.Chat.ID, ctx.Tr("wol_no_mac"))
		return
	}

	macClean := strings.ReplaceAll(macStr, ":", "")
	macClean = strings.ReplaceAll(macClean, "-", "")
	
	if len(macClean) != 12 {
		sendMarkdown(bot, msg.Chat.ID, fmt.Sprintf(ctx.Tr("wol_fail"), "Invalid MAC address format (must be 12 hex characters)"))
		return
	}

	macBytes, err := hex.DecodeString(macClean)
	if err != nil {
		sendMarkdown(bot, msg.Chat.ID, fmt.Sprintf(ctx.Tr("wol_fail"), err.Error()))
		return
	}

	var packet [102]byte
	for i := 0; i < 6; i++ {
		packet[i] = 0xFF
	}
	for i := 1; i <= 16; i++ {
		copy(packet[i*6:], macBytes)
	}

	addr, err := net.ResolveUDPAddr("udp", "255.255.255.255:9")
	if err != nil {
		sendMarkdown(bot, msg.Chat.ID, fmt.Sprintf(ctx.Tr("wol_fail"), err.Error()))
		return
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		sendMarkdown(bot, msg.Chat.ID, fmt.Sprintf(ctx.Tr("wol_fail"), err.Error()))
		return
	}
	defer conn.Close()

	_, err = conn.Write(packet[:])
	if err != nil {
		sendMarkdown(bot, msg.Chat.ID, fmt.Sprintf(ctx.Tr("wol_fail"), err.Error()))
		return
	}

	sendMarkdown(bot, msg.Chat.ID, fmt.Sprintf(ctx.Tr("wol_success"), macStr))
}

func (c *WolCmd) Description() string {
	return "Send a Wake-on-LAN magic packet to the configured MAC address"
}
