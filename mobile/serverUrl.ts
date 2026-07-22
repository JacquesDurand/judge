// Runtime-configurable server URL.
//
// Instead of baking the server address in at build time, the app lets the user
// enter it in a settings screen and remembers it (AsyncStorage). This way a
// single build works against any server — a LAN dev box, a hosted instance, a
// friend's — without rebuilding. api.ts reads getServerUrl() at call time.
import AsyncStorage from "@react-native-async-storage/async-storage";

import { API_BASE_URL } from "./config";

const STORAGE_KEY = "serverUrl";

// DEFAULT_SERVER_URL comes from the build-time env / localhost fallback. It is
// used until the user configures their own, and prefilled on the setup screen.
export const DEFAULT_SERVER_URL = API_BASE_URL;

let current = DEFAULT_SERVER_URL;

// normalizeUrl trims whitespace and any trailing slash so callers can always
// append "/chat" etc. Returns null if it does not look like an http(s) URL.
export function normalizeUrl(raw: string): string | null {
  const trimmed = raw.trim().replace(/\/+$/, "");
  if (!/^https?:\/\/.+/i.test(trimmed)) return null;
  return trimmed;
}

// getServerUrl returns the base URL to use for API calls (no trailing slash).
export function getServerUrl(): string {
  return current;
}

// loadServerUrl restores the saved URL into memory on app start. `configured` is
// false when nothing has been stored yet (first launch), so the caller can open
// the setup screen.
export async function loadServerUrl(): Promise<{ url: string; configured: boolean }> {
  try {
    const saved = await AsyncStorage.getItem(STORAGE_KEY);
    if (saved) {
      current = saved;
      return { url: saved, configured: true };
    }
  } catch {
    // Storage unavailable — fall back to the default.
  }
  return { url: current, configured: false };
}

// saveServerUrl validates, persists, and activates a new base URL. Returns the
// normalized value, or throws if it is not a valid http(s) URL.
export async function saveServerUrl(raw: string): Promise<string> {
  const url = normalizeUrl(raw);
  if (!url) {
    throw new Error("Adresse invalide. Elle doit commencer par http:// ou https://");
  }
  current = url;
  await AsyncStorage.setItem(STORAGE_KEY, url);
  return url;
}
