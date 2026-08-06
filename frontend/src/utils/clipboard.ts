export async function copyText(text: string) {
  if (!navigator.clipboard?.writeText) {
    throw new Error("Clipboard access requires a secure context.");
  }
  await navigator.clipboard.writeText(text);
}
