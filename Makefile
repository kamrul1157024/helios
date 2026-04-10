.PHONY: build frontend clean dev install uninstall test
.PHONY: apk apk-release apk-install apk-run apk-clean apk-device mobile

APK_DEBUG = mobile/build/app/outputs/flutter-apk/app-debug.apk
APK_RELEASE = mobile/build/app/outputs/flutter-apk/app-release.apk

build: frontend
	touch frontend.go
	go build -o helios ./cmd/helios/
	codesign -s - -f ./helios

install: build
	sudo cp helios /usr/local/bin/helios
	sudo codesign -s - -f /usr/local/bin/helios
	sudo xattr -dr com.apple.quarantine /usr/local/bin/helios
	@echo "helios installed to /usr/local/bin/helios"

uninstall:
	sudo rm -f /usr/local/bin/helios
	@echo "helios removed from /usr/local/bin"

frontend:
	rm -rf frontend/dist
	cd frontend && npm install && npm run build

clean:
	rm -f helios
	rm -rf frontend/dist frontend/node_modules

dev:
	cd frontend && npm run dev &
	go run ./cmd/helios/ daemon start

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
	cd mobile && flutter build apk --release
	mkdir -p ~/.helios
	cp $(APK_RELEASE) ~/.helios/helios.apk
	@echo "APK: $(APK_RELEASE)"
	@echo "Copied to ~/.helios/helios.apk"

## Install APK on connected device (builds first if needed)
apk-install: apk
	@adb devices | grep -q 'device$$' || (echo "No Android device connected. Connect via USB and enable USB debugging." && exit 1)
	adb install -r $(APK_DEBUG)
	@echo "Installed on device."

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

## Full build: frontend + Go binary + APK
mobile: build apk
	@echo "All built: helios binary + mobile APK"
