# Native apps

This directory holds the Supa Proto client apps built with Kotlin
Multiplatform. The Go module at the repository root remains the protocol core;
the apps reuse it through gomobile (`mobile/`) instead of reimplementing the
protocol in Kotlin. Android runs the Go engine in a `VpnService`; iOS runs it in
a Packet Tunnel extension that links only Swift and Go.

```text
shared/      KMP module shared by both apps (exported to iOS as Shared.framework)
androidApp/  Android app, Jetpack Compose
iosApp/      iOS app, SwiftUI, iOS 16+, Kotlin API surfaced through SKIE
```

## Requirements

- JDK 17 or newer and Go (version in `go.mod`)
- Android SDK with platform 37 and NDK r28c for `androidApp`; set
  `ANDROID_HOME` (or `ANDROID_NDK_HOME`) when building the Go core
- Xcode with an iOS 16+ runtime for `iosApp`; running the tunnel needs a device
  and a team with the Network Extension capability (Simulator cannot start it)

Kotlin and SKIE versions are pinned together in `gradle/libs.versions.toml`;
SKIE supports specific Kotlin releases, so upgrade them as a pair.

## Build

From the repository root, build the Go core first; the apps link
`apps/build/supaproto-core.aar` and `apps/build/SupaProtoCore.xcframework`:

```bash
scripts/build-mobile-core.sh all   # or: ios, android
```

Run the shared tests:

```bash
./gradlew :shared:allTests
```

Build the Android app:

```bash
./gradlew :androidApp:assembleDebug
```

Open `iosApp/iosApp.xcodeproj` in Xcode and run the `iosApp` scheme. The
"Compile Kotlin Framework" build phase calls
`./gradlew :shared:embedAndSignAppleFrameworkForXcode` before compiling Swift.

## Releases

`.github/workflows/release.yml` runs on every new commit on `main` (a push or a
merge): Go and shared-module tests first, then an Android APK and an unsigned
iOS IPA, then a GitHub release tagged with the next version.

- The version comes from the latest `vX.Y.Z` tag, incremented by one patch
  release; the first release is `0.1.0`. Put `[release:minor]` or
  `[release:major]` in a commit message to raise the minor or major version
  instead, or run the workflow by hand and pick the part to increment.
- The apps get that version at build time: Gradle properties `appVersionName`
  and `appVersionCode`, and Xcode's `MARKETING_VERSION` and
  `CURRENT_PROJECT_VERSION`. The build number is
  `major × 1,000,000 + minor × 1,000 + patch`. The repository keeps `0.1.0` as
  the local default.
- To sign the APK with a release key, add these repository secrets:
  `ANDROID_KEYSTORE_BASE64` (the keystore file, base64-encoded),
  `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS` and `ANDROID_KEY_PASSWORD`.
  Without them the release carries a debug-signed APK, which installs but
  cannot update an earlier release in place.
- The IPA is unsigned; re-sign it with a team that has the Network Extension
  capability before installing it on a device.
