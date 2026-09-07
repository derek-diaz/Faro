(() => {
  try {
    const mode = window.localStorage.getItem("faro-theme");
    const selected = mode === "light" || mode === "dark" || mode === "system" ? mode : "system";
    document.documentElement.dataset.theme = selected;
    document.documentElement.dataset.themeResolved = selected === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : selected;
    document.querySelector('meta[name="theme-color"]')?.setAttribute("content", document.documentElement.dataset.themeResolved === "dark" ? "#090f20" : "#f4f7fb");
  } catch {
    document.documentElement.dataset.theme = "system";
    document.documentElement.dataset.themeResolved = window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
    document.querySelector('meta[name="theme-color"]')?.setAttribute("content", document.documentElement.dataset.themeResolved === "dark" ? "#090f20" : "#f4f7fb");
  }
})();
