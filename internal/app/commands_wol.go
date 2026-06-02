package app

import (
	"encoding/hex"
	"fmt"
	"net"
	"strings"
)

type WolCmd struct{}

func (c *WolCmd) Handle(ctx *CommandContext) {
	macStr := strings.TrimSpace(ctx.Config.WakeOnLan.MacAddress)
	if macStr == "" {
		ctx.Reply(ctx.Tr("wol_no_mac"))
		return
	}

	// Clean MAC string
	macClean := strings.ReplaceAll(macStr, ":", "")
	macClean = strings.ReplaceAll(macClean, "-", "")
	
	if len(macClean) != 12 {
		ctx.Reply(fmt.Sprintf(ctx.Tr("wol_fail"), "Formato MAC address non valido nel config (deve essere lungo 12 caratteri esadecimali)"))
		return
	}

	macBytes, err := hex.DecodeString(macClean)
	if err != nil {
		ctx.Reply(fmt.Sprintf(ctx.Tr("wol_fail"), err.Error()))
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
		ctx.Reply(fmt.Sprintf(ctx.Tr("wol_fail"), err.Error()))
		return
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		ctx.Reply(fmt.Sprintf(ctx.Tr("wol_fail"), err.Error()))
		return
	}
	defer conn.Close()

	_, err = conn.Write(packet[:])
	if err != nil {
		ctx.Reply(fmt.Sprintf(ctx.Tr("wol_fail"), err.Error()))
		return
	}

	ctx.Reply(fmt.Sprintf(ctx.Tr("wol_success"), macStr))
}
