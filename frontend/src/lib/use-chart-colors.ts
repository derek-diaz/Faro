import { useMemo, useSyncExternalStore } from "react";

function subscribe(onChange: () => void) {
  const observer = new MutationObserver(onChange);
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme-resolved"] });
  return () => observer.disconnect();
}

function getTheme() {
  return document.documentElement.dataset.themeResolved ?? "light";
}

/** Canvas charts cannot inherit CSS colors; refresh their palette when appearance changes. */
export function useChartColors() {
  const theme = useSyncExternalStore(subscribe, getTheme);
  return useMemo(() => {
    const styles = getComputedStyle(document.documentElement);
    const color = (name: string) => styles.getPropertyValue(name).trim();
    return {
      accent: color("--accent"),
      blocked: color("--blocked"),
      grid: color("--border-subtle"),
      axis: color("--text-muted"),
      fill: color("--chart-fill"),
      surface: color("--surface"),
      cursor: color("--ink"),
    };
  }, [theme]);
}
