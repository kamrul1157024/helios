import java.util.Base64

plugins {
    id("com.android.application")
    id("kotlin-android")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

// `--dart-define=DISTRIBUTION=play` makes the Google Play build. Flutter hands
// the defines to Gradle base64-encoded, so the one switch that turns the Dart
// updater off also picks the Play application ID and manifest here — two knobs
// would be two things to forget. Flavors were the other way to split it, and
// they would have demanded matching Xcode schemes from the iOS build.
val dartDefines: Map<String, String> = (project.findProperty("dart-defines") as String?)
    ?.split(",")
    ?.map { String(Base64.getDecoder().decode(it)) }
    ?.associate { it.substringBefore("=") to it.substringAfter("=", "") }
    ?: emptyMap()
val isPlayBuild = dartDefines["DISTRIBUTION"] == "play"

android {
    namespace = "com.helios.helios"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        isCoreLibraryDesugaringEnabled = true
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = JavaVersion.VERSION_17.toString()
    }

    defaultConfig {
        // The sideloaded APK keeps the ID every existing install was made
        // with, or none of them could upgrade. Play was given its own, under
        // the owner's name: com.helios.app was already taken there.
        applicationId = if (isPlayBuild) "com.kamrul1157024.helios" else "com.helios.helios"
        minSdk = flutter.minSdkVersion
        targetSdk = flutter.targetSdkVersion
        versionCode = flutter.versionCode
        versionName = flutter.versionName
        multiDexEnabled = true
        manifestPlaceholders["appLabel"] = "helios"
    }

    val keystorePath = System.getenv("KEYSTORE_PATH")
    if (keystorePath != null) {
        signingConfigs {
            create("release") {
                storeFile = file(keystorePath)
                storePassword = System.getenv("STORE_PASSWORD")
                keyAlias = System.getenv("KEY_ALIAS")
                keyPassword = System.getenv("KEY_PASSWORD")
            }
        }
    }

    // Overlaid on the main manifest, and only for Play: it strips what Play
    // policy forbids an app like this one. Build-type source sets rather than
    // main, because only an overlay can remove an entry.
    if (isPlayBuild) {
        sourceSets.getByName("debug").manifest.srcFile("src/play/AndroidManifest.xml")
        sourceSets.getByName("release").manifest.srcFile("src/play/AndroidManifest.xml")
    }

    buildTypes {
        // Debug builds carry their own application ID so a locally built app
        // installs alongside the release one instead of colliding with it: the
        // two are signed with different keys, so they can never upgrade each
        // other. The launcher label is suffixed too, so the two icons on the
        // home screen are told apart.
        debug {
            applicationIdSuffix = ".dev"
            versionNameSuffix = "-dev"
            manifestPlaceholders["appLabel"] = "helios dev"
        }
        release {
            signingConfig = if (keystorePath != null)
                signingConfigs.getByName("release")
            else
                signingConfigs.getByName("debug")
        }
    }
}

flutter {
    source = "../.."
}

dependencies {
    coreLibraryDesugaring("com.android.tools:desugar_jdk_libs:2.1.4")
}
