#!/bin/bash
set -euo pipefail

APP_NAME="helios"
APP_DIR="mobile/build/macos/Build/Products/Release"
APP_BUNDLE="${APP_DIR}/${APP_NAME}.app"
DMG_PATH="helios.dmg"
VOLUME_NAME="Helios"
TMP_DMG="/tmp/helios-tmp.dmg"

if [ ! -d "$APP_BUNDLE" ]; then
    echo "Error: ${APP_BUNDLE} not found. Run 'make dmg' to build first."
    exit 1
fi

echo "Creating DMG from ${APP_BUNDLE}..."

# Clean up any previous artifacts
rm -f "$DMG_PATH" "$TMP_DMG"

# Create a temporary DMG
SIZE=$(du -sm "$APP_BUNDLE" | cut -f1)
SIZE=$((SIZE + 10)) # add 10MB padding
hdiutil create -size "${SIZE}m" -fs HFS+ -volname "$VOLUME_NAME" "$TMP_DMG"

# Mount, copy app, add Applications symlink
MOUNT_DIR=$(hdiutil attach "$TMP_DMG" | grep "/Volumes/" | sed 's/.*\/Volumes/\/Volumes/')
cp -R "$APP_BUNDLE" "$MOUNT_DIR/"
ln -s /Applications "$MOUNT_DIR/Applications"

# Unmount
hdiutil detach "$MOUNT_DIR"

# Convert to compressed DMG
hdiutil convert "$TMP_DMG" -format UDZO -o "$DMG_PATH"
rm -f "$TMP_DMG"

echo "DMG created: ${DMG_PATH}"
