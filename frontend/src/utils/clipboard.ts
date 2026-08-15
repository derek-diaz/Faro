export async function copyText(text: string) {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return;
    }
  } catch {
    // The async Clipboard API is unavailable on plain HTTP and can also be
    // rejected by browser permissions. Try the user-initiated legacy API below.
  }

  copyWithLegacyAPI(text);
}

function copyWithLegacyAPI(text: string) {
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.top = "0";
  textarea.style.left = "-9999px";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);

  const selection = document.getSelection();
  const selectedRange = selection?.rangeCount ? selection.getRangeAt(0) : null;
  let copied = false;
  try {
    textarea.select();
    textarea.setSelectionRange(0, textarea.value.length);
    copied = document.execCommand("copy");
  } finally {
    textarea.remove();
    if (selection) {
      selection.removeAllRanges();
      if (selectedRange) selection.addRange(selectedRange);
    }
  }

  if (!copied) {
    throw new Error("Clipboard access is unavailable.");
  }
}
