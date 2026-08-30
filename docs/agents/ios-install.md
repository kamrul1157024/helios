# Procedure: install the iOS app on a cabled iPhone

The user has connected an iPhone by cable and asked you to install the Helios app on it.
There is no `make` target for iOS — build it from `mobile/` with Flutter.

## Facts about this repo

- Flutter project root: `mobile/`, Xcode project at `mobile/ios/Runner.xcodeproj`
- Bundle id: `com.helios.helios` (`mobile/ios/Runner.xcodeproj/project.pbxproj:375`)
- Signing style is Automatic; `DEVELOPMENT_TEAM` is not committed, so the first build on
  a new machine fails until a team is selected
- Deployment target: iOS 13.0
- Dart SDK constraint: `^3.8.0`

## Steps

1. Confirm the device is visible and note its id:

   ```bash
   cd mobile && flutter devices
   ```

   If no iPhone appears, stop and tell the user to unlock the phone and tap **Trust**.
   Do not fall back to the simulator — a simulator build cannot be used away from the
   Mac, which is the whole point of the app.

2. Install to the device:

   ```bash
   cd mobile && flutter run --release -d <device-id>
   ```

3. If the build fails with a signing error, the project has no team set. Tell the user
   to open `mobile/ios/Runner.xcworkspace` in Xcode, select the **Runner** target,
   and pick their Apple ID under **Signing & Capabilities → Team**. Then repeat step 2.
   Do not commit the resulting `DEVELOPMENT_TEAM` value — it is personal to that Apple
   ID.

4. The first launch on the phone is blocked by iOS. Tell the user to open
   **Settings → General → VPN & Device Management**, then trust their developer
   certificate.

## Rules

- Never remove the cable requirement from the docs. Wireless install needs the device
  paired for wireless debugging first, which is not part of this flow.
- A free Apple ID signs a build that expires after seven days. If the user reports the
  app refusing to open after about a week, that is the cause — reinstall.
