import { LoaderCircle, Trash2, X } from "lucide-react";
import { useEffect, useId, useRef, type ReactNode } from "react";

type ConfirmDialogProps = {
  title: string;
  body: string;
  confirmLabel: string;
  busyLabel?: string;
  busy?: boolean;
  detail?: ReactNode;
  onCancel: () => void;
  onConfirm: () => void;
};

export function ConfirmDialog({ title, body, confirmLabel, busyLabel = "Removing…", busy = false, detail, onCancel, onConfirm }: ConfirmDialogProps) {
  const titleID = useId();
  const bodyID = useId();
  const cancelRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    cancelRef.current?.focus();
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape" && !busy) onCancel();
    }
    window.addEventListener("keydown", onKeyDown);
    return () => {
      document.body.style.overflow = previousOverflow;
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [busy, onCancel]);

  return (
    <div className="confirm-dialog-backdrop" role="presentation" onMouseDown={() => { if (!busy) onCancel(); }}>
      <section className="confirm-dialog" role="alertdialog" aria-modal="true" aria-labelledby={titleID} aria-describedby={bodyID} onMouseDown={(event) => event.stopPropagation()}>
        <header>
          <span className="confirm-dialog-icon"><Trash2 size={20} /></span>
          <div><h2 id={titleID}>{title}</h2><p id={bodyID}>{body}</p></div>
          <button type="button" className="icon-button" aria-label="Close confirmation" disabled={busy} onClick={onCancel}><X size={17} /></button>
        </header>
        {detail}
        <footer>
          <button ref={cancelRef} type="button" className="secondary" disabled={busy} onClick={onCancel}>Cancel</button>
          <button type="button" className="danger confirm-dialog-action" disabled={busy} onClick={onConfirm}>{busy && <LoaderCircle className="spinning" size={16} />}<span>{busy ? busyLabel : confirmLabel}</span></button>
        </footer>
      </section>
    </div>
  );
}
