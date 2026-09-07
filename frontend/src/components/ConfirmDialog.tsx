import { LoaderCircle, Trash2 } from "lucide-react";
import { useRef, type ReactNode } from "react";
import { AlertDialog, AlertDialogContent, AlertDialogHeader, AlertDialogTitle, AlertDialogDescription, AlertDialogFooter, AlertDialogCancel, AlertDialogAction, AlertDialogMedia } from "./ui/alert-dialog";

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
  const cancelRef = useRef<HTMLButtonElement>(null);
  return <AlertDialog open onOpenChange={(open, event) => { if (!open) { if (busy) event.cancel(); else onCancel(); } }}>
    <AlertDialogContent className="max-h-[85vh] overflow-y-auto" initialFocus={autoFocusCancel ? cancelRef : undefined}>
      <AlertDialogHeader>
        <AlertDialogMedia>{icon ?? <Trash2 size={20} />}</AlertDialogMedia>
        <AlertDialogTitle>{title}</AlertDialogTitle>
        <AlertDialogDescription>{body}</AlertDialogDescription>
      </AlertDialogHeader>
      {detail}
      <AlertDialogFooter>
        <AlertDialogCancel ref={cancelRef} disabled={busy}>Cancel</AlertDialogCancel>
        <AlertDialogAction variant="destructive" disabled={busy} onClick={onConfirm}>{busy && <LoaderCircle className="spinning" size={16} />}{busy ? busyLabel : confirmLabel}</AlertDialogAction>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>;
}
