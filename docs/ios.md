# iOS

There is no prebuilt iOS build and no App Store listing. Apple only runs an app you
signed yourself, so the app is built on your Mac and pushed straight to your iPhone.

1. Connect the iPhone to the Mac **with a cable**. Wireless install does not work for
   the first install.
2. Unlock the phone and tap **Trust** when it asks about the computer.
3. Open Xcode once and sign in with your Apple ID, so a signing team exists.
4. Ask your coding agent, in this repo, to build and install the iOS app on the
   connected device.

The agent follows [docs/agents/ios-install.md](agents/ios-install.md), which has the
build commands and the signing details.

A free Apple ID works. The build it produces expires after seven days, so repeat the
steps when the app stops opening. A paid Apple Developer account raises that to a year.
