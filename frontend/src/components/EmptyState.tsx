import type { ReactNode } from "react";

type EmptyStateProps = {
  title: string;
  body: string;
  action?: ReactNode;
};

export function EmptyState({ title, body, action }: EmptyStateProps) {
  return (
    <div className="empty-state friendly-empty">
      <strong>{title}</strong>
      <span>{body}</span>
      {action}
    </div>
  );
}
