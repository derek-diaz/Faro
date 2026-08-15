import { ExternalLink, X } from "lucide-react";
import { useEffect, useRef } from "react";
import type { AppVersion, ReleaseInfo } from "../api/client";
import { BrandLogo } from "./BrandLogo";

type AboutDialogProps = Readonly<{
  open: boolean;
  onClose: () => void;
  appVersion: AppVersion | null;
  releaseUpdate: ReleaseInfo | null;
}>;

const repositoryURL = "https://github.com/derek-diaz/Faro";
const creatorURL = "https://github.com/derek-diaz";

export function AboutDialog({ open, onClose, appVersion, releaseUpdate }: AboutDialogProps) {
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (!open) return undefined;
    const previousOverflow = document.body.style.overflow;
    const previousPaddingRight = document.body.style.paddingRight;
    const scrollbarWidth = window.innerWidth - document.documentElement.clientWidth;
    document.body.style.overflow = "hidden";
    if (scrollbarWidth > 0) document.body.style.paddingRight = `${scrollbarWidth}px`;
    closeButtonRef.current?.focus();

    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") onCloseRef.current();
    }

    window.addEventListener("keydown", onKeyDown);
    return () => {
      document.body.style.overflow = previousOverflow;
      document.body.style.paddingRight = previousPaddingRight;
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  if (!open) return null;

  return (
    <dialog
      className="modal-backdrop about-modal-backdrop"
      open
      aria-modal="true"
      aria-labelledby="about-faro-title"
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <article className="about-modal">
        <header className="about-modal-header">
          <button ref={closeButtonRef} className="icon-button" type="button" onClick={onClose} aria-label="Close About Faro">
            <X size={18} />
          </button>
        </header>

        <div className="about-modal-body">
          <div className="about-modal-hero">
            <BrandLogo className="about-modal-logo" />
            <span className="about-modal-eyebrow">About Faro</span>
            <h2 id="about-faro-title">Understand your network.</h2>
            <p>Self-hosted DNS that shows you what your devices are doing while keeping your data on your network.</p>
          </div>

          <div className="about-modal-meta" aria-label="Faro release details">
            <span>Version {appVersion?.display ?? "Checking…"}</span>
            <span>Apache-2.0</span>
          </div>

          {releaseUpdate && (
            <div className="about-modal-update">
              <div>
                <strong>Faro {releaseUpdate.display} is available.</strong>
                <span>You are running {appVersion?.display ?? "an earlier version"}.</span>
              </div>
              <a href={releaseUpdate.url} target="_blank" rel="noreferrer">
                View release <ExternalLink size={14} />
              </a>
            </div>
          )}

          <nav className="about-modal-links" aria-label="Faro resources">
            <a href={`${repositoryURL}/tree/main/docs`} target="_blank" rel="noreferrer">Documentation <ExternalLink size={14} /></a>
            <a href={repositoryURL} target="_blank" rel="noreferrer">GitHub <ExternalLink size={14} /></a>
            <a href={`${repositoryURL}/blob/main/LICENSE`} target="_blank" rel="noreferrer">License <ExternalLink size={14} /></a>
          </nav>
        </div>

        <footer className="about-modal-footer">
          <span>Built by <a href={creatorURL} target="_blank" rel="noreferrer">Derek Diaz Correa</a> in Puerto Rico 🇵🇷</span>
          <button type="button" className="secondary" onClick={onClose}>Done</button>
        </footer>
      </article>
    </dialog>
  );
}
