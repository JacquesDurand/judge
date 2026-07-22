// Default base URL of the Go server (cmd/server).
//
// This is only the *default* — the actual URL is set by the user in-app (a
// settings screen) and persisted, so one build works against any server without
// rebuilding. See serverUrl.ts. This value is used until the user configures one,
// and as the prefill on the setup screen.
//
// It reads EXPO_PUBLIC_API_BASE_URL (a build-time env var, e.g. from a gitignored
// `.env.local`) if present, else localhost. Handy so the simulator/web default is
// right without touching the settings screen; keep any personal URL out of the
// repo.
export const API_BASE_URL =
  process.env.EXPO_PUBLIC_API_BASE_URL ?? "http://localhost:8090";
