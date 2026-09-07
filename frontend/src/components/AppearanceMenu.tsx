import { Moon, Sun, SunMoon } from "lucide-react";
import type { ThemeMode } from "@/theme";
import { Button } from "./ui/button";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuGroup, DropdownMenuLabel, DropdownMenuRadioGroup, DropdownMenuRadioItem } from "./ui/dropdown-menu";

type AppearanceMenuProps = Readonly<{
  themeMode: ThemeMode;
  onThemeModeChange: (mode: ThemeMode) => void;
  className?: string;
}>;

export function AppearanceMenu({ themeMode, onThemeModeChange, className }: AppearanceMenuProps) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="ghost" size="icon" className={className} aria-label="Choose appearance" title="Choose appearance" />}>
        {themeIcon(themeMode, 18)}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-40" aria-label="Appearance">
        <DropdownMenuGroup><DropdownMenuLabel>Appearance</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={themeMode} onValueChange={(value) => onThemeModeChange(value as ThemeMode)}>
        {(["system", "light", "dark"] as ThemeMode[]).map((mode) => (
          <DropdownMenuRadioItem
            key={mode}
            value={mode}
            closeOnClick
          >
            {themeIcon(mode, 15)}
            <span>{themeModeLabel(mode)}</span>
          </DropdownMenuRadioItem>
        ))}
        </DropdownMenuRadioGroup></DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
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
