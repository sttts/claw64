package relay

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type monitorSymbolTable map[string]uint16

func writeStallDump(debugDir, monitorAddr, symPath, reason string, pendingChunk []byte, relayState []string) (string, error) {
	if debugDir == "" || monitorAddr == "" {
		return "", fmt.Errorf("debug dump disabled")
	}
	if err := os.MkdirAll(debugDir, 0o755); err != nil {
		return "", fmt.Errorf("create debug dir: %w", err)
	}

	filename := filepath.Join(debugDir, "stall-"+time.Now().Format("20060102-150405")+".log")
	f, err := os.Create(filename)
	if err != nil {
		return "", fmt.Errorf("create debug log: %w", err)
	}
	defer f.Close()

	fmt.Fprintf(f, "reason: %s\n", reason)
	fmt.Fprintf(f, "time: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(f, "monitor: %s\n", monitorAddr)
	fmt.Fprintf(f, "sym: %s\n", symPath)
	fmt.Fprintf(f, "pending_chunk_len: %d\n", len(pendingChunk))
	if len(pendingChunk) > 0 {
		fmt.Fprintf(f, "pending_chunk_text: %q\n", string(pendingChunk))
	}
	for _, line := range relayState {
		fmt.Fprintln(f, line)
	}
	fmt.Fprintln(f)

	conn, err := net.DialTimeout("tcp", monitorAddr, 2*time.Second)
	if err != nil {
		return filename, fmt.Errorf("connect monitor: %w", err)
	}
	defer conn.Close()

	commands := []string{
		"r",
		"m 029b 029e",
		"m 00f7 00fa",
		"m 0400 07ff",
		"m cf60 cf9f",
		"m cf00 cfff",
		"m cbcc cc05",
		"m c000 cfff",
	}

	if syms, err := loadMonitorSymbols(symPath); err == nil {
		if lo, hi, ok := symbolRange(syms,
			"frame_len",
			"agent_state",
			"ready_timer",
			"send_pos",
			"send_total",
			"ack_pos",
			"ack_total",
			"state_pending",
			"state_len",
			"state_src_lo",
			"state_src_hi",
			"llm_pending",
			"llm_len",
			"prompt_pending",
			"result_pending",
			"text_pending",
			"text_len",
			"ack_pending",
			"deferred_ack",
			"tx_ack_wait",
			"tx_next_id",
			"tx_ack_id",
			"tx_ack_timer",
			"tx_retries",
			"tx_service_busy",
			"prompt_sent",
			"busy",
			"busy_timer",
		); ok {
			commands = append(commands, fmt.Sprintf("m %04x %04x", lo, hi))
		}
		if lo, hi, ok := symbolRange(syms,
			"USERQ_STAGE_LEN",
			"USERQ_HEAD_PTR",
			"USERQ_TAIL_PTR",
			"USERQ_COUNT_PTR",
		); ok {
			commands = append(commands, fmt.Sprintf("m %04x %04x", lo, hi))
		}
		if lo, hi, ok := symbolRange(syms,
			"USERQ_BASE",
			"USERQ_LIMIT",
		); ok && hi > lo {
			slotSize := (hi - lo) / 3
			if slotSize > 0 {
				for i := uint16(0); i < 3; i++ {
					slotLo := lo + i*slotSize
					slotHi := slotLo + 0x1f
					if max := lo + (i+1)*slotSize - 1; slotHi > max {
						slotHi = max
					}
					commands = append(commands, fmt.Sprintf("m %04x %04x", slotLo, slotHi))
				}
			}
		}
	}

	if !hasMonitorCommand(commands, "m 9500 9503") {
		commands = append(commands, "m 9500 9503")
	}
	if !hasGuardedQueueSlotDumps(commands) {
		commands = append(commands,
			"m 9200 921f",
			"m 9300 931f",
			"m 9400 941f",
		)
	}

	for _, cmd := range commands {
		fmt.Fprintf(f, ">>> %s\n", cmd)
		out, err := runMonitorCommand(conn, cmd)
		if err != nil {
			fmt.Fprintf(f, "error: %v\n\n", err)
			continue
		}
		f.WriteString(out)
		if !strings.HasSuffix(out, "\n") {
			f.WriteString("\n")
		}
		f.WriteString("\n")
	}

	return filename, nil
}

func runMonitorCommand(conn net.Conn, cmd string) (string, error) {
	if _, err := fmt.Fprintf(conn, "%s\n", cmd); err != nil {
		return "", err
	}

	var out strings.Builder
	buf := make([]byte, 8192)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
			return out.String(), err
		}
		n, err := conn.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
		}
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			return out.String(), err
		}
	}
	return out.String(), nil
}

func loadMonitorSymbols(path string) (monitorSymbolTable, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	syms := monitorSymbolTable{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, ".label ") {
			continue
		}
		line = strings.TrimPrefix(line, ".label ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		value, err := strconv.ParseUint(strings.TrimPrefix(parts[1], "$"), 16, 16)
		if err != nil {
			continue
		}
		syms[parts[0]] = uint16(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return syms, nil
}

func symbolRange(syms monitorSymbolTable, names ...string) (uint16, uint16, bool) {
	var addrs []uint16
	for _, name := range names {
		addr, ok := syms[name]
		if !ok {
			continue
		}
		addrs = append(addrs, addr)
	}
	if len(addrs) == 0 {
		return 0, 0, false
	}
	slices.Sort(addrs)
	return addrs[0], addrs[len(addrs)-1], true
}

func hasMonitorCommand(commands []string, want string) bool {
	for _, cmd := range commands {
		if cmd == want {
			return true
		}
	}
	return false
}

func hasGuardedQueueSlotDumps(commands []string) bool {
	return hasMonitorCommand(commands, "m 9200 921f") &&
		hasMonitorCommand(commands, "m 9300 931f") &&
		hasMonitorCommand(commands, "m 9400 941f")
}
