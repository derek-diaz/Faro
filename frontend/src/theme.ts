export type ThemeMode = "system" | "light" | "dark";

const THEME_STORAGE_KEY = "faro-theme";

export function readThemeMode(): ThemeMode {
  try {
    const stored = window.localStorage.getItem(THEME_STORAGE_KEY);
    if (stored === "light" || stored === "dark" || stored === "system") return stored;
  } catch {
    // Private browsing and storage-restricted environments should still render.
  }
  return "system";
}

export function applyThemeMode(mode: ThemeMode) {
  const resolved = mode === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : mode;
  document.documentElement.dataset.theme = mode;
  document.documentElement.dataset.themeResolved = resolved;
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", resolved === "dark" ? "#0e1923" : "#eaf0f4");
}

export function persistThemeMode(mode: ThemeMode) {
  applyThemeMode(mode);
  try {
    window.localStorage.setItem(THEME_STORAGE_KEY, mode);
  } catch {
    // The active theme still applies for this session when storage is unavailable.
  }
}
