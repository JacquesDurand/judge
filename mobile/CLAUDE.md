# Mobile app (Expo) — notes for working in this directory

- **Pinned to Expo SDK 54** (`expo ~54`, `react-native 0.81`, `react 19.1`) to match
  the Expo Go build in use. Do **not** bump the SDK unless a matching Expo Go
  version is available, or switch to a dev build (EAS). Note that
  `create-expo-app@latest` scaffolds the newest SDK — downgrade with
  `npm install expo@~54.0.0 && npx expo install --fix`.
- Uses only **core React Native + `fetch`**, plus `react-native-safe-area-context`
  (bundled in Expo Go). No other native modules, so it runs in Expo Go without a
  custom build. Streaming uses `XMLHttpRequest` (RN `fetch` can't read a body
  incrementally).
- The server URL is the single line in `config.ts`.
- When touching Expo-specific APIs, check the **SDK 54** docs
  (https://docs.expo.dev/versions/v54.0.0/), not the latest.
- Type-check with `npx tsc --noEmit`.
