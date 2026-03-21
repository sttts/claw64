# Claw64 Build System
# ====================
#
# Prerequisites:
#   brew install --cask vice    # C64 emulator
#   java (any JDK/JRE)          # for KickAssembler (downloaded automatically)

VICE        ?= x64sc

# VICE RS232 flags: map userport to TCP socket
VICE_RS     = -rsdev1 "127.0.0.1:25232" -rsdev1baud 2400 -rsuser -rsuserdev 0

# KickAssembler (auto-downloaded)
KICKASS_DIR  = build/kickassembler
KICKASS_JAR  = $(KICKASS_DIR)/KickAss.jar
KICKASS_URL  = https://theweb.dk/KickAssembler/KickAssembler.zip
KICKASS_ZIP  = build/KickAssembler.zip

# Source files
ASM_SRC     = c64/agent.asm
ASM_OUT     = c64/agent.prg

.PHONY: assemble vice bridge test-serial clean clean-all

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

# launch VICE with the agent and RS232 enabled
vice: assemble
	$(VICE) $(VICE_RS) -autostartprgmode 1 $(ASM_OUT)

# run the Go bridge
bridge:
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
