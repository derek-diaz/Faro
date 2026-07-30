export async function copyText(text: string) {
  let clipboardError: unknown;
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch (caught) {
      clipboardError = caught;
    }
  }

  // navigator.clipboard is commonly unavailable when Faro is opened from a
  // private IP over HTTP. Keep a local-only fallback for that deployment mode.
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.left = "-9999px";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.focus({ preventScroll: true });
  textarea.select();
  textarea.setSelectionRange(0, textarea.value.length);
  try {
    if (document.execCommand("copy")) return;
  } finally {
    textarea.remove();
  }

  throw clipboardError instanceof Error ? clipboardError : new Error("Clipboard access is unavailable");
}
