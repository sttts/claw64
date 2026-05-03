# Claw64 Build System
# ====================
#
# Prerequisites:
#   brew install --cask vice    # C64 emulator
#   java (any JDK/JRE)          # for KickAssembler (downloaded automatically)

VICE        ?= x64sc

# Per-worktree port file — allocates sticky serial + monitor ports so
# multiple worktrees can run VICE concurrently without collisions.
PORT_FILE = .ports

# KickAssembler (auto-downloaded)
KICKASS_DIR  = build/kickassembler
KICKASS_JAR  = $(KICKASS_DIR)/KickAss.jar
KICKASS_URL  = https://theweb.dk/KickAssembler/KickAssembler.zip
KICKASS_ZIP  = build/KickAssembler.zip

# Source files
ASM_SRC     = c64/agent.asm
ASM_OUT     = c64/agent.prg
LOADER_SRC  = c64/loader.asm
LOADER_OUT  = cmd/claw64-bridge/claw64.prg
ECHO_SRC    = c64/echotest.asm
ECHO_OUT    = c64/echotest.prg
VEC_SRC     = c64/vectest.asm
VEC_OUT     = c64/vectest.prg

BURNIN_REPEAT ?= 3

.PHONY: assemble assets echotest vectest vice vice-vec vice-echo bridge run test test-serial burnin burnin-repeat burnin-direct burnin-overlap ports kill clean clean-all clean-ports

define KILL_PORTS
	@. ./$(PORT_FILE); \
	for PORT in $$SERIAL_PORT $$MONITOR_PORT; do \
	  PIDS=$$(lsof -ti :$$PORT 2>/dev/null); \
	  if [ -n "$$PIDS" ]; then \
	    kill $$PIDS 2>/dev/null || true; \
	    sleep 0.2; \
	    PIDS=$$(lsof -ti :$$PORT 2>/dev/null); \
	    if [ -n "$$PIDS" ]; then \
	      kill -9 $$PIDS 2>/dev/null || true; \
	    fi; \
	    for _ in 1 2 3 4 5 6 7 8 9 10; do \
	      lsof -ti :$$PORT 2>/dev/null >/dev/null || break; \
	      sleep 0.1; \
	    done; \
	  fi; \
	done
endef

define RUN_BURNIN
	STATUS=0; \
	STALL_BEFORE=$$(ls -t debug/stall-*.log 2>/dev/null | head -1); \
	go run ./cmd/claw64-bridge --vice-bin $(VICE) burnin $(1) || STATUS=$$?; \
	if [ "$$STATUS" -ne 0 ]; then \
	  echo "burnin failed: scenario=$(1)" >&2; \
	  STALL_AFTER=$$(ls -t debug/stall-*.log 2>/dev/null | head -1); \
	  if [ -n "$$STALL_AFTER" ] && [ "$$STALL_AFTER" != "$$STALL_BEFORE" ]; then \
	    echo "new stall dump: $$STALL_AFTER" >&2; \
	  elif [ -n "$$STALL_AFTER" ]; then \
	    echo "new stall dump: none" >&2; \
	    echo "latest existing stall dump: $$STALL_AFTER" >&2; \
	  else \
	    echo "new stall dump: none" >&2; \
	  fi; \
	  exit $$STATUS; \
	fi
endef

# download KickAssembler if not present
$(KICKASS_JAR):
	@mkdir -p build
	curl -sSL -o $(KICKASS_ZIP) $(KICKASS_URL)
	unzip -o -q $(KICKASS_ZIP) -d $(KICKASS_DIR)
	@rm -f $(KICKASS_ZIP)
	@test -f $(KICKASS_JAR) || { echo "ERROR: KickAss.jar not found after unzip"; exit 1; }

# allocate two free TCP ports and persist them for this worktree
$(PORT_FILE):
	@SERIAL=$$(python3 -c "import socket; s=socket.socket(); s.bind(('',0)); print(s.getsockname()[1]); s.close()"); \
	 MONITOR=$$(python3 -c "import socket; s=socket.socket(); s.bind(('',0)); print(s.getsockname()[1]); s.close()"); \
	 echo "SERIAL_PORT=$$SERIAL" > $(PORT_FILE); \
	 echo "MONITOR_PORT=$$MONITOR" >> $(PORT_FILE); \
	 echo "Allocated ports: serial=$$SERIAL monitor=$$MONITOR"

# regenerate C64 binary assets from source PNGs (requires Pillow)
assets:
	python3 c64/tools/png_to_c64.py

# assemble the C64 loader (includes agent.asm via #import)
assemble: $(KICKASS_JAR)
	java -jar $(KICKASS_JAR) -o $(ASM_OUT) $(ASM_SRC)
	java -jar $(KICKASS_JAR) -o $(LOADER_OUT) $(LOADER_SRC)

# assemble the echo test
echotest: $(KICKASS_JAR)
	java -jar $(KICKASS_JAR) -o $(ECHO_OUT) $(ECHO_SRC)

# launch VICE with the agent and RS232 enabled
vice: assemble $(PORT_FILE)
	$(KILL_PORTS)
	@. ./$(PORT_FILE); \
	echo "VICE: serial=$$SERIAL_PORT monitor=$$MONITOR_PORT"; \
	$(VICE) \
	  -rsdev1 "127.0.0.1:$$SERIAL_PORT" -userportdevice 2 -rsuserdev 0 -rsuserbaud 2400 \
	  -remotemonitor -remotemonitoraddress "127.0.0.1:$$MONITOR_PORT" \
	  -autostart $(LOADER_OUT)

# assemble the vector test
vectest: $(KICKASS_JAR)
	java -jar $(KICKASS_JAR) -o $(VEC_OUT) $(VEC_SRC)

# launch VICE with vector test (to find which vectors fire at READY prompt)
vice-vec: vectest $(PORT_FILE)
	@. ./$(PORT_FILE); \
	$(VICE) \
	  -rsdev1 "127.0.0.1:$$SERIAL_PORT" -userportdevice 2 -rsuserdev 0 -rsuserbaud 2400 \
	  -autostart $(VEC_OUT)

# launch VICE with the echo test
vice-echo: echotest $(PORT_FILE)
	@. ./$(PORT_FILE); \
	$(VICE) \
	  -rsdev1 "127.0.0.1:$$SERIAL_PORT" -userportdevice 2 -rsuserdev 0 -rsuserbaud 2400 \
	  -autostartprgmode 1 $(ECHO_OUT)

# launch everything via the bridge CLI. By default, it spawns VICE itself.
run: assemble $(PORT_FILE)
	$(KILL_PORTS)
	@. ./$(PORT_FILE); \
	go run ./cmd/claw64-bridge \
	  --serial-addr "127.0.0.1:$$SERIAL_PORT" \
	  --monitor-addr "127.0.0.1:$$MONITOR_PORT" \
	  --vice-bin $(VICE) stdin

# run the Go bridge without spawning VICE
bridge: $(PORT_FILE)
	$(KILL_PORTS)
	@. ./$(PORT_FILE); \
	go run ./cmd/claw64-bridge \
	  --serial-addr "127.0.0.1:$$SERIAL_PORT" \
	  --monitor-addr "127.0.0.1:$$MONITOR_PORT" \
	  --spawn-vice=false stdin

# run all Go package tests
test:
	go test ./...

# run the serial test tool (TCP server on port 25232)
test-serial:
	cd tools && go run serialtest.go

# run all live burn-in scenarios used by the developer gate
burnin: assemble
	@$(call RUN_BURNIN,gate)

# repeat the full live burn-in gate to catch timing flakes
burnin-repeat: assemble
	@for RUN in $$(seq 1 $(BURNIN_REPEAT)); do \
	  echo "burnin repeat $$RUN/$(BURNIN_REPEAT)"; \
	  $(call RUN_BURNIN,gate); \
	done

# run the default direct execution burn-in
burnin-direct: burnin-direct-exec

# run the largest running-overlap burn-in
burnin-overlap: burnin-overlap-running24

# run one live burn-in scenario
burnin-%: assemble
	@$(call RUN_BURNIN,$*)

# show allocated ports for this worktree
ports: $(PORT_FILE)
	@. ./$(PORT_FILE); \
	echo "serial=$$SERIAL_PORT monitor=$$MONITOR_PORT"

# kill VICE/bridge running on this worktree's ports
kill: $(PORT_FILE)
	$(KILL_PORTS)
	@. ./$(PORT_FILE); \
	echo "killed processes on serial=$$SERIAL_PORT monitor=$$MONITOR_PORT"

# remove build artifacts
clean:
	rm -f $(ASM_OUT) c64/*.sym

# remove port allocation
clean-ports:
	rm -f $(PORT_FILE)

# remove everything including downloaded tools
clean-all: clean clean-ports
	rm -rf build/
