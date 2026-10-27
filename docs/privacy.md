# Helios privacy policy

Last updated: 27 October 2026

This policy covers the Helios app for Android and iOS. Helios is an open-source
project. Its source code is at <https://github.com/kamrul1157024/helios>.

## Summary

The Helios developers collect no data. The app has no accounts, no analytics,
no advertising, and no crash reporting. The app sends your data only to the
Helios daemon that you run on your own computer.

## What the app sends, and to where

| Data | Where it goes | Why |
| --- | --- | --- |
| Prompts, approvals, files, and images that you send to a session | Your own Helios daemon, at the address that you enter or scan | This is the purpose of the app |
| A device ID and a public key | Your own Helios daemon | The daemon uses them to recognize your phone when you pair it |
| Voice input, when you tap the microphone | The speech recognition service on your phone, which is usually Google's | Your phone turns your speech into text. Helios receives only the text |
| A request for the list of releases (the GitHub APK only) | GitHub | It checks for a newer version. The Google Play version does not do this |

The Helios developers do not run a server. They cannot see any of this data.

## Permissions

- **Camera**: The app scans the QR code that pairs it with your daemon. The app
  does not store or send the camera image.
- **Microphone**: The app uses it for voice input, only after you tap the
  microphone button. It stops when you stop talking.
- **Notifications and foreground service**: The app keeps one connection open
  to your daemon. This lets an approval request reach your phone when the app is
  closed.
- **Photos and files**: The app reads only the files that you choose to attach.

## Data on your phone

The app keeps its pairing keys in the secure storage of your phone. It keeps its
settings in local app storage. When you uninstall the app, your phone deletes
this data.

## Children

Helios is a developer tool. It is not for children.

## Changes

This file records each change to this policy. Its history is at
<https://github.com/kamrul1157024/helios/commits/main/docs/privacy.md>.

## Contact

Open an issue at <https://github.com/kamrul1157024/helios/issues>.
