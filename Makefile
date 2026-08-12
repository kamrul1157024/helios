.PHONY: build clean install uninstall test
.PHONY: apk apk-release apk-install apk-run apk-debug apk-clean apk-device mobile
.PHONY: dmg dmg-dev changelog release
.PHONY: desktop desktop-dev desktop-test desktop-app desktop-install desktop-clean

VERSION = 0.2.5
REPO = kamrul1157024/helios
UNAME_S := $(shell uname -s)
APK_DEBUG = mobile/build/app/outputs/flutter-apk/app-debug.apk
APK_RELEASE = mobile/build/app/outputs/flutter-apk/app-release.apk
DMG_PATH = helios.dmg

build:
	go build -o helios ./cmd/helios/
ifeq ($(UNAME_S),Darwin)
	codesign -s - -f ./helios
endif

install: build
ifeq ($(UNAME_S),Linux)
	# Linux returns ETXTBSY when overwriting a mapped binary; rename over it instead
	sudo cp helios /usr/local/bin/helios.new
	sudo mv -f /usr/local/bin/helios.new /usr/local/bin/helios
else
	sudo cp helios /usr/local/bin/helios
endif
ifeq ($(UNAME_S),Darwin)
	sudo codesign -s - -f /usr/local/bin/helios
	sudo xattr -d com.apple.quarantine /usr/local/bin/helios 2>/dev/null || true
endif
	@echo "helios installed to /usr/local/bin/helios"

uninstall:
	sudo rm -f /usr/local/bin/helios
	@echo "helios removed from /usr/local/bin"

clean:
	rm -f helios

test:
	go test ./...

# ─── Mobile (Flutter) ───────────────────────────────────────────

## Build debug APK (skips if APK already exists, use apk-rebuild to force)
apk:
	@if [ -f "$(APK_DEBUG)" ]; then \
		echo "APK already exists: $(APK_DEBUG)"; \
		read -p "Rebuild? [y/N] " yn; \
		case $$yn in [yY]*) cd mobile && flutter build apk --debug ;; *) echo "Skipped." ;; esac; \
	else \
		cd mobile && flutter build apk --debug; \
	fi
	@if [ -f "$(APK_DEBUG)" ]; then \
		mkdir -p ~/.helios; \
		cp $(APK_DEBUG) ~/.helios/helios.apk; \
		echo "APK: $(APK_DEBUG)"; \
		echo "Copied to ~/.helios/helios.apk"; \
	fi

## Force rebuild debug APK
apk-rebuild:
	cd mobile && flutter build apk --debug
	mkdir -p ~/.helios
	cp $(APK_DEBUG) ~/.helios/helios.apk
	@echo "APK: $(APK_DEBUG)"
	@echo "Copied to ~/.helios/helios.apk"

## Build release APK
apk-release:
	cd mobile && flutter build apk --release --build-name=$(VERSION) --build-number=$(shell git rev-list --count HEAD)
	mkdir -p ~/.helios
	cp $(APK_RELEASE) ~/.helios/helios.apk
	@echo "APK: $(APK_RELEASE)"
	@echo "Copied to ~/.helios/helios.apk"

## Install APK on connected device (builds first if needed)
apk-install: apk
	@adb devices | grep -q 'device$$' || (echo "No Android device connected. Connect via USB and enable USB debugging." && exit 1)
	adb install -r $(APK_DEBUG)
	@echo "Installed on device."

## Force rebuild debug APK and install on first connected device
apk-debug:
	@adb devices | grep -q 'device$$' || (echo "No Android device connected. Connect via USB and enable USB debugging." && exit 1)
	cd mobile && flutter build apk --debug
	adb install -r $(APK_DEBUG)
	@echo "Built and installed on device."

## Build, install, and launch on device
apk-run: apk-install
	adb shell am start -n com.helios.helios/.MainActivity
	@echo "Launched helios on device."

## Run on connected device with hot reload (flutter run)
apk-dev:
	@adb devices | grep -q 'device$$' || (echo "No Android device connected." && exit 1)
	cd mobile && flutter run

## Show connected devices
apk-device:
	@adb devices -l
	@echo ""
	@flutter devices

## Clean mobile build artifacts
apk-clean:
	cd mobile && flutter clean
	@echo "Mobile build cleaned."

## Full build: Go binary + APK
mobile: build apk
	@echo "All built: helios binary + mobile APK"

# ─── macOS (Flutter Desktop) ───────────────────────────────────

## Build macOS app and package as DMG
dmg:
	cd mobile && flutter build macos --release
	./scripts/create-dmg.sh
	mkdir -p ~/.helios
	cp $(DMG_PATH) ~/.helios/helios.dmg
	@echo "DMG: $(DMG_PATH)"
	@echo "Copied to ~/.helios/helios.dmg"

## Run macOS app in debug mode
dmg-dev:
	cd mobile && flutter run -d macos

# ─── Desktop (Electron) ─────────────────────────────────────────

# electron-builder names the DMG after the arch it was built for, and the
# default is the host's.
DESKTOP_VERSION = $(shell sed -n 's/.*"version": "\(.*\)".*/\1/p' desktop/package.json | head -1)
DESKTOP_ARCH = $(if $(filter arm64,$(shell uname -m)),arm64,x64)
DESKTOP_DMG = desktop/release/helios-desktop-$(DESKTOP_VERSION)-$(DESKTOP_ARCH).dmg
# electron-builder suffixes the staging directory with the arch, except on the
# x64 build, where it is bare `mac`.
DESKTOP_APP = desktop/release/$(if $(filter arm64,$(DESKTOP_ARCH)),mac-arm64,mac)/Helios.app

# make runs recipes under /bin/sh, which never sources a profile, so an nvm
# install is invisible unless the parent shell already exported it.
NPM = $(shell command -v npm 2>/dev/null || ls -d $$HOME/.nvm/versions/node/*/bin/npm 2>/dev/null | tail -1)
# npm's shebang is `env node`, so its directory has to be on PATH too.
NODE_ENV_PATH = PATH="$(patsubst %/npm,%,$(NPM)):$$PATH"

## Install node deps if missing, then bundle main, preload and renderer
desktop:
	@test -n "$(NPM)" || (echo "npm not found — install Node 20+ (or run 'nvm use' first)" >&2 && exit 1)
	@if [ ! -d desktop/node_modules ]; then cd desktop && $(NODE_ENV_PATH) npm install; fi
	cd desktop && $(NODE_ENV_PATH) npm run typecheck && $(NODE_ENV_PATH) npm run build
	@echo "Desktop bundles: desktop/dist"

## Run the desktop app against the daemon on this machine
desktop-dev: desktop
	cd desktop && $(NODE_ENV_PATH) npm run dev

## Frame-codec tests (shares golden fixtures with the Go protocol tests)
desktop-test:
	cd desktop && $(NODE_ENV_PATH) npm test

## Package the desktop app (macOS: DMG in desktop/release)
desktop-app: desktop
	cd desktop && $(NODE_ENV_PATH) npm run dist
	@if [ -f "$(DESKTOP_DMG)" ]; then \
		mkdir -p ~/.helios; \
		cp "$(DESKTOP_DMG)" ~/.helios/helios-desktop.dmg; \
		echo "DMG: $(DESKTOP_DMG)"; \
		echo "Copied to ~/.helios/helios-desktop.dmg"; \
	else \
		echo "Built, but $(DESKTOP_DMG) is missing — see desktop/release"; \
	fi

## Build the desktop app and install it into /Applications (macOS)
desktop-install: desktop-app
ifneq ($(UNAME_S),Darwin)
	@echo "make desktop-install is macOS-only; on Linux install the AppImage or .deb from desktop/release" >&2
	@exit 1
else
	@test -d "$(DESKTOP_APP)" || (echo "$(DESKTOP_APP) not found — see desktop/release" >&2 && exit 1)
	# Replaced rather than copied over: a copy leaves the previous build's files
	# behind inside the bundle.
	rm -rf /Applications/Helios.app
	cp -R "$(DESKTOP_APP)" /Applications/Helios.app
	xattr -dr com.apple.quarantine /Applications/Helios.app 2>/dev/null || true
	@echo "Helios.app installed to /Applications"
endif

desktop-clean:
	rm -rf desktop/dist desktop/release
	@echo "Desktop build artifacts removed."

# ─── Release ─────────────────────────────────────────────────────

## Generate changelog from conventional commits since the last tag
changelog:
	@./scripts/changelog.sh

## Create a GitHub release with available artifacts (APK required, DMG optional)
## Changelog is auto-generated from conventional commits since the last tag.
## Fails if a release with the same tag already exists.
release: apk-release
	@echo "Creating GitHub release v$(VERSION)..."
	@if gh release view v$(VERSION) --repo $(REPO) > /dev/null 2>&1; then \
		echo "Error: Release v$(VERSION) already exists. Bump VERSION in the Makefile." >&2; \
		exit 1; \
	fi
	@./scripts/changelog.sh > /tmp/helios-changelog.md
	@echo "--- Changelog ---"
	@cat /tmp/helios-changelog.md
	@echo "---"
	cp $(APK_RELEASE) helios.apk
	@ASSETS="helios.apk"; \
	if [ -f "$(DMG_PATH)" ]; then \
		ASSETS="$$ASSETS $(DMG_PATH)"; \
		echo "Including DMG in release"; \
	else \
		echo "DMG not found — releasing APK only (run 'make dmg' first to include it)"; \
	fi; \
	gh release create v$(VERSION) \
		--repo $(REPO) \
		--title "helios v$(VERSION)" \
		--notes-file /tmp/helios-changelog.md \
		$$ASSETS
	rm -f helios.apk /tmp/helios-changelog.md
	@echo ""
	@echo "Release created: https://github.com/$(REPO)/releases/tag/v$(VERSION)"
	@echo "APK download:    https://github.com/$(REPO)/releases/download/v$(VERSION)/helios.apk"
	@if [ -f "$(DMG_PATH)" ]; then \
		echo "DMG download:    https://github.com/$(REPO)/releases/download/v$(VERSION)/helios.dmg"; \
	fi
