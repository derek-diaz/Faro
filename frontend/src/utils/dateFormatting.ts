export function formatTime(timestamp: string) {
  return new Date(timestamp).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export function formatDate(timestamp: string) {
  const date = new Date(timestamp);
  const today = new Date();
  return date.toDateString() === today.toDateString() ? "Today" : date.toLocaleDateString([], { month: "short", day: "numeric" });
}
