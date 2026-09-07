import type { ReactNode } from "react";
import { Dialog } from "@base-ui/react/dialog";
import { cn } from "../lib/utils";

/** Product dialog layouts share dismissal, focus containment and scroll locking. */
export function DialogSurface({ children, className, onClose, busy = false, ...props }: {
  children: ReactNode;
  className?: string;
  onClose: () => void;
  busy?: boolean;
  "aria-label"?: string;
  "aria-labelledby"?: string;
  initialFocus?: Dialog.Popup.Props["initialFocus"];
}) {
  return <Dialog.Root open onOpenChange={(open, event) => {
    if (!open) { if (busy) event.cancel(); else onClose(); }
  }}>
    <Dialog.Portal>
      <Dialog.Backdrop className="faro-dialog-backdrop" />
      <Dialog.Popup className={cn("faro-dialog-layer", className)} {...props}>{children}</Dialog.Popup>
    </Dialog.Portal>
  </Dialog.Root>;
}
