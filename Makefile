# Claw64 Build System
# ====================
#
# Prerequisites:
#   brew install --cask vice    # C64 emulator
#   java (any JDK/JRE)          # for KickAssembler (downloaded automatically)

VICE        ?= x64sc

# VICE RS232 flags: map userport to TCP socket
# VICE acts as TCP client — bridge must listen on port 25232 FIRST
# -rsdev1: TCP endpoint VICE connects to when C64 opens RS232
# -rsuserdev 0: map userport to rsdev1
# -rsuserbaud: emulated baud rate (must match C64 software)
VICE_RS     = -rsdev1 "127.0.0.1:25232" -userportdevice 2 -rsuserdev 0 -rsuserbaud 2400
VICE_MON    = -remotemonitor -remotemonitoraddress 127.0.0.1:6510

# KickAssembler (auto-downloaded)
KICKASS_DIR  = build/kickassembler
KICKASS_JAR  = $(KICKASS_DIR)/KickAss.jar
KICKASS_URL  = https://theweb.dk/KickAssembler/KickAssembler.zip
KICKASS_ZIP  = build/KickAssembler.zip

# Source files
ASM_SRC     = c64/agent.asm
ASM_OUT     = c64/agent.prg
ECHO_SRC    = c64/echotest.asm
ECHO_OUT    = c64/echotest.prg
VEC_SRC     = c64/vectest.asm
VEC_OUT     = c64/vectest.prg

.PHONY: assemble echotest vice vice-echo bridge run test-serial clean clean-all

# download KickAssembler if not present
$(KICKASS_JAR):
	@mkdir -p build
	curl -sSL -o $(KICKASS_ZIP) $(KICKASS_URL)
	unzip -o -q $(KICKASS_ZIP) -d $(KICKASS_DIR)
	@rm -f $(KICKASS_ZIP)
	@test -f $(KICKASS_JAR) || { echo "ERROR: KickAss.jar not found after unzip"; exit 1; }

# assemble the C64 agent
assemble: $(KICKASS_JAR)
	java -jar $(KICKASS_JAR) -o $(ASM_OUT) $(ASM_SRC)

# assemble the echo test
echotest: $(KICKASS_JAR)
	java -jar $(KICKASS_JAR) -o $(ECHO_OUT) $(ECHO_SRC)

# launch VICE with the agent and RS232 enabled
# Agent is auto-loaded but not auto-run. Type SYS 49152 to start.
vice: assemble
	$(VICE) $(VICE_RS) $(VICE_MON) -autostart $(ASM_OUT) -keybuf "sys 49152\n"

# assemble the vector test
vectest: $(KICKASS_JAR)
	java -jar $(KICKASS_JAR) -o $(VEC_OUT) $(VEC_SRC)

# launch VICE with vector test (to find which vectors fire at READY prompt)
vice-vec: vectest
	$(VICE) $(VICE_RS) -autostart $(VEC_OUT)

# launch VICE with the echo test
vice-echo: echotest
	$(VICE) $(VICE_RS) -autostartprgmode 1 $(ECHO_OUT)

# launch everything: build, start bridge in background, start VICE
# Bridge listens first, then VICE connects when C64 opens RS232.
run: assemble
	@-lsof -ti :25232 2>/dev/null | xargs kill 2>/dev/null; true
	@-pkill -f "$(VICE).*$(ASM_OUT)" 2>/dev/null; true
	@# Start VICE in background (output suppressed), poll until bridge is listening
	@(while ! nc -z 127.0.0.1 25232 2>/dev/null; do sleep 0.2; done; \
	  $(VICE) $(VICE_RS) -autostart $(ASM_OUT) -keybuf "sys 49152\n" \
	  > /dev/null 2>&1) &
	cd bridge && go run .

# run the Go bridge (default: uses claude CLI for LLM, stdin for chat)
bridge:
	@-lsof -ti :25232 2>/dev/null | xargs kill 2>/dev/null; true
	cd bridge && go run .

# run the serial test tool (TCP server on port 25232)
test-serial:
	cd tools && go run serialtest.go

# remove build artifacts
clean:
	rm -f $(ASM_OUT) c64/*.sym

# remove everything including downloaded tools
clean-all: clean
	rm -rf build/
