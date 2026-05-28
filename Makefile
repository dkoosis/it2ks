BIN_DIR := $(HOME)/.local/bin
LAUNCH_DIR := $(HOME)/Library/LaunchAgents
PLIST := com.dk.it2ks.plist
INSTALLED_PLIST := $(LAUNCH_DIR)/$(PLIST)

.PHONY: build test install uninstall clean

build:
	go build -o it2ks ./cmd/it2ks

test:
	go test ./...

install: build
	mkdir -p $(BIN_DIR)
	cp it2ks $(BIN_DIR)/it2ks
	mkdir -p $(LAUNCH_DIR)
	sed "s|__HOME__|$(HOME)|g" $(PLIST) > $(INSTALLED_PLIST)
	launchctl unload $(INSTALLED_PLIST) 2>/dev/null || true
	launchctl load $(INSTALLED_PLIST)
	@echo "it2ks installed and loaded."

uninstall:
	launchctl unload $(INSTALLED_PLIST) 2>/dev/null || true
	rm -f $(INSTALLED_PLIST)
	rm -f $(BIN_DIR)/it2ks
	@echo "it2ks uninstalled."

clean:
	rm -f it2ks
