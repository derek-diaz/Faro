import { LoaderCircle, Trash2, X } from "lucide-react";
import { useEffect, useId, useRef, type ReactNode } from "react";

type ConfirmDialogProps = {
  readonly title: string;
  readonly body: string;
  readonly confirmLabel: string;
  readonly busyLabel?: string;
  readonly busy?: boolean;
  readonly detail?: ReactNode;
  readonly icon?: ReactNode;
  readonly autoFocusCancel?: boolean;
  readonly onCancel: () => void;
  readonly onConfirm: () => void;
};

export function ConfirmDialog({ title, body, confirmLabel, busyLabel = "Removing…", busy = false, detail, icon, autoFocusCancel = true, onCancel, onConfirm }: ConfirmDialogProps) {
  const titleID = useId();
  const bodyID = useId();
  const cancelRef = useRef<HTMLButtonElement>(null);
  const busyRef = useRef(busy);
  const onCancelRef = useRef(onCancel);
  busyRef.current = busy;
  onCancelRef.current = onCancel;

  useEffect(() => {
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    if (autoFocusCancel) cancelRef.current?.focus();
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape" && !busyRef.current) onCancelRef.current();
    }
    window.addEventListener("keydown", onKeyDown);
    return () => {
      document.body.style.overflow = previousOverflow;
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [autoFocusCancel]);

  return (
    <dialog open className="confirm-dialog-backdrop" aria-modal="true" aria-labelledby={titleID} aria-describedby={bodyID}>
      <button type="button" className="confirm-dialog-backdrop-close" aria-label="Close confirmation" disabled={busy} onClick={onCancel} />
      <div className="confirm-dialog">
        <header>
          <span className="confirm-dialog-icon">{icon ?? <Trash2 size={20} />}</span>
          <div><h2 id={titleID}>{title}</h2><p id={bodyID}>{body}</p></div>
          <button type="button" className="icon-button" aria-label="Close confirmation" disabled={busy} onClick={onCancel}><X size={17} /></button>
        </header>
        {detail}
        <footer>
          <button ref={cancelRef} type="button" className="secondary" disabled={busy} onClick={onCancel}>Cancel</button>
          <button type="button" className="danger confirm-dialog-action" disabled={busy} onClick={onConfirm}>{busy && <LoaderCircle className="spinning" size={16} />}<span>{busy ? busyLabel : confirmLabel}</span></button>
        </footer>
      </div>
    </dialog>
  );
}
