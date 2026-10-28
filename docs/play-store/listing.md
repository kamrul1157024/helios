# Google Play listing: Helios

Copy these texts into Play Console. The limits are Google's.

## App details

| Field | Text |
|---|---|
| Package name | `com.kamrul1157024.helios` (the GitHub APK stays `com.helios.helios`) |
| App name (30 characters at most) | Helios: AI Coding Agents |
| Short description (80 characters at most) | Watch and approve your Claude Code and Codex sessions from your phone. |
| Category | Tools |
| Tags | Developer tools, Productivity |
| Email | Your support email (Play Console shows it on the listing) |
| Website | https://github.com/kamrul1157024/helios |
| Privacy policy | https://github.com/kamrul1157024/helios/blob/main/docs/privacy.md |

## Full description (4000 characters at most)

```
Helios puts your AI coding agents in your pocket.

Claude Code and OpenAI Codex run in a terminal with no screen of their own. The Helios daemon runs each session on your own computer and keeps them all in one place. This app shows those sessions on your phone, and tells you the moment one of them needs you.

What you can do
- See every session on every machine, and which ones are working, waiting, or done.
- Approve or deny a tool call straight from the notification.
- Read the live transcript, and send a prompt by typing or by voice.
- Open the real terminal of any session.
- Browse the files of a project, and see what Git changed.
- Start a new session, or fork one into a branch of its own.

Your machine, your data
Helios has no cloud, no accounts, and no subscription. The app talks only to the daemon that you run. The Helios developers collect nothing.

Before you start
This app needs the free Helios daemon on a Mac or Linux computer. Install it with one command from https://github.com/kamrul1157024/helios, then scan the QR code that "helios start" shows. A built-in tunnel lets your phone reach the daemon from anywhere.

Helios is open source.

Claude Code is a product of Anthropic. Codex is a product of OpenAI. Helios is an independent project and is not affiliated with or endorsed by either company.
```

## Release notes for the first release

```
The first release on Google Play.
```

## Graphics

| Asset | File |
|---|---|
| App icon, 512 × 512 | `icon-512.png` |
| Feature graphic, 1024 × 500 | `feature-graphic.png` |
| Phone screenshots, 2:1, 24-bit PNG | `screenshots/01-sessions.png` … `06-permission-mode.png`, in this order |

`scripts/play_graphics.py` makes all of them. The screenshots are the README's phone screenshots, padded to 2:1 because Play refuses anything taller. The two notification screenshots in `docs/assets/mobile` are left out: they show other companies' app icons on a home screen.

## Permission declarations

### Foreground service: special use

Play Console → App content → Foreground service permissions → "Special use".

Description:

```
Helios runs a foreground service only while the user has turned on "Watch in background" in the app's notification settings. The service holds one event stream open to the user's own Helios daemon, a program that runs on the user's own computer. When an AI coding agent on that computer stops and waits for the user's approval, the request reaches the phone at once, and the user can approve or deny it from the notification. The service shows a persistent notification while it runs. The user turns it off with the same switch. dataSync does not fit: Android 15 stops a dataSync service after six hours a day, and the approval requests come at any hour. No other foreground service type fits.
```

Demo video (upload to YouTube as unlisted, about 30 seconds):

1. In Settings → Notifications, turn on "Watch in background". The persistent notification appears.
2. Close the app. On the computer, start a Claude Code session that asks for a permission.
3. The approval notification arrives. Tap "Approve". The session continues on the computer.
4. Turn "Watch in background" off. The persistent notification disappears.

### Battery optimization exemption

No form in Play Console. The app asks for `REQUEST_IGNORE_BATTERY_OPTIMIZATIONS` only when the user taps "Fix" under "Battery optimisation is on", beside "Watch in background". It never asks on launch. If a reviewer asks: without the exemption, Doze closes the event stream and approval requests arrive hours late, which breaks the app's core function.

### Removed from the Play build

The Play build (`DISTRIBUTION=play`) removes these permissions, so they need no declaration:

- `REQUEST_INSTALL_PACKAGES`: the GitHub APK's updater uses it. Play updates the Play build.
- `USE_FULL_SCREEN_INTENT`: Play allows it only for calling and alarm apps. Approval alerts show as heads-up notifications instead.

## App access

All features need a Helios daemon that the reviewer can reach. Choose one:

- **Instructions only.** Paste: "Install the daemon on a Mac or Linux computer with the command at https://github.com/kamrul1157024/helios. Run `helios start`, choose a tunnel, and scan the QR code with the app." Google may reject this as too hard to test.
- **A demo host (stronger).** Run a daemon on a spare machine with a tunnel for the review period. Paste its pairing URL and the steps to reach a session.

## Data safety

| Question | Answer |
|---|---|
| Does the app collect or share user data? | No. The app sends data only to the Helios daemon that the user runs on their own computer, at an address the user enters. Neither the developer nor a third party receives it. |
| Data types collected | None |
| Data types shared | None |
| Can users ask for their data to be deleted? | Yes. Uninstalling the app deletes everything on the phone. There is no server copy. |

Notes for the form:

- Voice input uses Android's own speech recognition service. The app receives only the text.
- The app has no account, so the account deletion requirement does not apply.

## Ads

The app contains no ads.

## Content rating (IARC questionnaire)

| Topic | Answer |
|---|---|
| Category | Utility, productivity, communication or other |
| Violence, sexual content, profanity, drugs, gambling | No |
| User-generated content shared with other users | No |
| Users can talk to each other | No |
| Shares the user's location | No |
| Lets users buy digital goods | No |
| Unrestricted internet access (a web browser) | No |

Expected rating: Everyone / PEGI 3.

## Target audience

18 and older. Helios is a tool for software developers. It is not made for children and does not appeal to them.

## AI policy

- Helios contains no AI model and generates no content. It is a remote control for AI coding agents that the user installs and runs on their own computer, with their own accounts.
- The text that the app shows comes from the user's own sessions and is shown only to that user. Nobody else sees it.
- Claude Code and Codex appear only by name, in plain text. The app and the listing use no logos of other companies, and the listing says that Helios is not affiliated with Anthropic or OpenAI.
