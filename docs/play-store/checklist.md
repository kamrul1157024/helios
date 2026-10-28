# Publish Helios on Google Play: checklist

Do these steps in order. The texts and files are in this folder:

- [listing.md](listing.md): store texts, declarations, Data safety and content rating answers.
- `icon-512.png`, `feature-graphic.png`, and `screenshots/`.
- Privacy policy: https://github.com/kamrul1157024/helios/blob/main/docs/privacy.md

## 1. Developer account

1. Open https://play.google.com/console and create a developer account. The fee is 25 USD, one time.
2. Complete Google's identity check. It can take a few days.
3. New personal accounts must run a closed test before production: **at least 12 testers, opted in, for 14 days in a row**. Start this early (step 5). It decides the earliest date you can publish.

## 2. Create the app

1. Click **Create app**.
2. App name: **Helios: AI Coding Agents**. Default language: English (United States).
3. Type: **App**. Free.
4. Accept the declarations.

The package name `com.kamrul1157024.helios` comes from the first bundle that you upload. Play locks it after that upload.

## 3. Choose how Google signs the app (Play App Signing)

Play App Signing is required. Google keeps the key that signs the app for users. You upload with an "upload key". The release workflow signs the bundle with `helios-release.jks` (the `KEYSTORE_BASE64` secret), so that key is the upload key.

Choose **"Let Google create and manage the app signing key"**. The Play build has its own package name (`com.kamrul1157024.helios`), so it never upgrades the GitHub APK (`com.helios.helios`). Keeping one signature for both builds gains nothing. Decide before the first upload; you cannot change it later.

Keep a backup of `helios-release.jks` and its passwords. If you lose the upload key, Google can reset it, but that takes days.

## 4. Get the app bundle

The release workflow attaches `helios.aab` (the Google Play build) to every GitHub release, next to `helios.apk`.

- The Play build has no GitHub updater, no "install apps" permission, and no full-screen intent. Updates come from Google Play.
- Download it from the newest release on https://github.com/kamrul1157024/helios/releases.
- Or build it yourself: `make aab-release VERSION=x.y.z` with `KEYSTORE_PATH`, `KEY_ALIAS`, `KEY_PASSWORD`, and `STORE_PASSWORD` set.

Each upload needs a higher version code. Every new release from the workflow has one (`git rev-list --count HEAD`).

## 5. Closed test (12 testers, 14 days)

1. Go to **Test and release → Testing → Closed testing**. Create a track.
2. Add testers: an email list or a Google Group with at least 12 people.
3. Upload `helios.aab` to the track. Add release notes.
4. Send the opt-in link to the testers. Each must accept it and install the app from Play.
5. Wait 14 days. Keep at least 12 testers opted in the whole time.
6. Then apply for production access in the Dashboard. Google asks short questions about the test.

## 6. Store listing

**Grow users → Store presence → Main store listing.** Copy from [listing.md](listing.md):

1. Short and full description.
2. App icon `icon-512.png` and feature graphic `feature-graphic.png`.
3. Phone screenshots from `screenshots/` (at least 2, at most 8).
4. Category **Tools**, contact email, website, privacy policy URL.

## 7. App content forms

**Policy and programs → App content.** Answers are in [listing.md](listing.md).

1. Privacy policy: the URL above.
2. Ads: no ads.
3. App access: the app needs a Helios daemon. Give the reviewers instructions or a demo host. This is the step most likely to fail review.
4. Content rating: fill in the IARC questionnaire.
5. Target audience: 18 and older.
6. Data safety: no data collected or shared.
7. Government app: no. Financial features: none. Health: no. News: no.
8. Foreground service: "Special use", with the text and a demo video.

There is no full-screen intent form: the Play build does not ask for that permission.

## 8. Production release

1. After Google approves production access, go to **Production → Create new release**.
2. Upload the newest `helios.aab` (or promote the closed test release).
3. Choose the countries.
4. Send the release for review. The first review can take several days.

## After launch

- For each new version: run the release workflow, then upload the new `helios.aab` to Play.
- Users of the Play version get updates from Play. The app offers them with Google's in-app update screen, and Settings has a row to check.
- Users of the GitHub APK keep getting updates from GitHub.
