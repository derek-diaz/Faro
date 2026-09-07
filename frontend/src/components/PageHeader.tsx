import type { ReactNode } from "react";

export function PageHeader({ title, description, actions }: { title: string; description: string; actions?: ReactNode }) {
  return (
    <header className="topbar">
      <div className="page-heading">
        <h1>{title}</h1>
        <p>{description}</p>
      </div>
      <div className="topbar-actions">{actions}</div>
    </header>
  );
}
