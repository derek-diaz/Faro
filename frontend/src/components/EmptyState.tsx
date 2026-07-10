type EmptyStateProps = {
  title: string;
  body: string;
};

export function EmptyState({ title, body }: EmptyStateProps) {
  return (
    <div className="empty-state friendly-empty">
      <strong>{title}</strong>
      <span>{body}</span>
    </div>
  );
}
