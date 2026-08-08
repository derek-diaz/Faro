type LoadingStateProps = Readonly<{
  title: string;
  description: string;
  className?: string;
}>;

export function LoadingState({ title, description, className = "" }: LoadingStateProps) {
  const loadingClassName = ["diagnostics-loading", className].filter(Boolean).join(" ");

  return (
    <div className={loadingClassName} role="status" aria-live="polite">
      <div className="diagnostics-loading-status">
        <span className="diagnostics-loading-spinner" aria-hidden="true" />
        <div>
          <strong>{title}</strong>
          <span>{description}</span>
        </div>
      </div>
      <div className="diagnostics-loading-summary" aria-hidden="true">
        <span />
        <span />
        <span />
        <span />
      </div>
      <div className="diagnostics-loading-file" aria-hidden="true">
        <span />
        <span />
        <span />
      </div>
    </div>
  );
}
