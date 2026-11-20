# omahass: build, test, preview and (when you're ready) install.
#
# Builds run under a systemd memory cap so they can't take the machine down.

# A mise-installed Go if there is one (its shim fails without a pinned
# version), otherwise whatever `go` is on PATH.
GO      ?= $(shell d=$$(mise where go@latest 2>/dev/null) && [ -x "$$d/bin/go" ] && echo "$$d/bin/go" || command -v go)
PYTHON  ?= $(HOME)/.cache/omahass-test/.venv/bin/python
CAP     := systemd-run --user --scope -q -p MemoryMax=3G -p MemorySwapMax=0
BIN     := build/omahassd
PLUGIN  := $(HOME)/.config/omarchy/plugins/omahass

.PHONY: build test test-go test-ha preview install uninstall

build:
	cd daemon && $(CAP) env GOFLAGS=-p=2 $(GO) build -trimpath -ldflags "-s -w" -o ../$(BIN) .

test: test-go test-ha

test-go:
	cd daemon && $(CAP) env GOFLAGS=-p=2 $(GO) test -race -count=1 ./...

# Needs the Home Assistant test harness:
#   uv venv --python 3.13 ~/.cache/omahass-test/.venv
#   uv pip install --python ~/.cache/omahass-test/.venv/bin/python pytest-homeassistant-custom-component
test-ha: build
	$(CAP) $(PYTHON) -m pytest -q -p no:cacheprovider

# Off-screen render of the panel against a fake Home Assistant.
preview: build
	$(CAP) tests/preview/run.sh docs/screenshots

install: build
	install -Dm755 $(BIN) $(HOME)/.local/bin/omahassd
	install -Dm644 packaging/omahassd.service $(HOME)/.config/systemd/user/omahassd.service
	ln -sfn $(CURDIR)/plugin/omahass $(PLUGIN)
	systemctl --user daemon-reload
	systemctl --user enable --now omahassd
	omarchy plugin enable omahass
	@echo "Pairing key: $$($(HOME)/.local/bin/omahassd key)"

uninstall:
	-systemctl --user disable --now omahassd
	rm -f $(HOME)/.local/bin/omahassd $(HOME)/.config/systemd/user/omahassd.service
	rm -f $(PLUGIN)
	systemctl --user daemon-reload
