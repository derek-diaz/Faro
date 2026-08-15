import { Moon, Sun, SunMoon } from "lucide-react";
import type { ThemeMode } from "../theme";

type AppearanceMenuProps = Readonly<{
  themeMode: ThemeMode;
  onThemeModeChange: (mode: ThemeMode) => void;
  className?: string;
}>;

export function AppearanceMenu({ themeMode, onThemeModeChange, className }: AppearanceMenuProps) {
  const menuClassName = ["theme-menu", className].filter(Boolean).join(" ");
  return (
    <details className={menuClassName} onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) event.currentTarget.removeAttribute("open"); }}>
      <summary className="icon-button" aria-label="Choose appearance" title="Choose appearance">
        {themeIcon(themeMode, 18)}
      </summary>
      <div className="theme-menu-popover" role="menu" aria-label="Appearance">
        <span>Appearance</span>
        {(["system", "light", "dark"] as ThemeMode[]).map((mode) => (
          <button
            key={mode}
            type="button"
            role="menuitemradio"
            aria-checked={themeMode === mode}
            className={themeMode === mode ? "selected" : ""}
            onClick={(event) => {
              onThemeModeChange(mode);
              event.currentTarget.closest("details")?.removeAttribute("open");
            }}
          >
            {themeIcon(mode, 15)}
            <span>{themeModeLabel(mode)}</span>
          </button>
        ))}
      </div>
    </details>
  );
}

function themeIcon(mode: ThemeMode, size: number) {
  switch (mode) {
    case "dark":
      return <Moon size={size} />;
    case "light":
      return <Sun size={size} />;
    default:
      return <SunMoon size={size} />;
  }
}

function themeModeLabel(mode: ThemeMode) {
  switch (mode) {
    case "dark":
      return "Dark";
    case "light":
      return "Light";
    default:
      return "System";
  }
}
