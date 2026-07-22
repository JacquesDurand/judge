# Mobile app (Expo) — notes for working in this directory

- **Pinned to Expo SDK 54** (`expo ~54`, `react-native 0.81`, `react 19.1`) to match
  the Expo Go build in use. Do **not** bump the SDK unless a matching Expo Go
  version is available, or switch to a dev build (EAS). Note that
  `create-expo-app@latest` scaffolds the newest SDK — downgrade with
  `npm install expo@~54.0.0 && npx expo install --fix`. (The SDK pin only matters
  for **Expo Go**; a standalone EAS build bundles its own runtime and isn't tied
  to it — but there's no reason to bump right now.)
- Uses only **core React Native + `fetch`**, plus `react-native-safe-area-context`
  (bundled in Expo Go). No other native modules, so it runs in Expo Go without a
  custom build. Streaming uses `XMLHttpRequest` (RN `fetch` can't read a body
  incrementally).
- **Standalone builds** use EAS (`eas.json`): profile `preview` produces a
  downloadable **APK**, `production` an AAB, `development` a dev client (needed
  later for the OIDC login flow). Build in the cloud with
  `npx eas-cli build -p android --profile preview` — no local Android SDK/JDK
  needed. `android/`/`ios/` are generated (gitignored); this stays a managed
  workflow.
- The server URL is **set by the user in-app** (settings screen behind the header
  gear) and persisted with AsyncStorage — see `serverUrl.ts`; `api.ts` reads
  `getServerUrl()` at call time. `config.ts` / `EXPO_PUBLIC_API_BASE_URL` only
  provide the *default* used until one is configured. So one build works against
  any server, and no personal URL is committed.
- The Android build enables cleartext HTTP (`expo-build-properties` in `app.json`)
  so the in-app URL can be a plain-HTTP LAN server. Changing that plugin config is
  a native change → needs a rebuild.
- When touching Expo-specific APIs, check the **SDK 54** docs
  (https://docs.expo.dev/versions/v54.0.0/), not the latest.
- Type-check with `npx tsc --noEmit`.
