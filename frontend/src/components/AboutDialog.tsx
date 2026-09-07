import { DialogSurface } from "./DialogSurface";
import { Button } from "./ui/button";
import { ExternalLink, X } from "lucide-react";
import { useRef } from "react";
import type { AppVersion, ReleaseInfo } from "@/api/client";
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
  if (!open) return null;

  return (
    <DialogSurface
      onClose={onClose}
      initialFocus={closeButtonRef}
      className="modal-backdrop about-modal-backdrop"
      aria-labelledby="about-faro-title"
    >
      <article className="about-modal">
        <header className="about-modal-header">
          <Button variant="ghost" size="icon" ref={closeButtonRef} className="icon-button" type="button" onClick={onClose} aria-label="Close About Faro">
            <X size={18} />
          </Button>
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
          <Button variant="outline" type="button" className="secondary" onClick={onClose}>Done</Button>
        </footer>
      </article>
    </DialogSurface>
  );
}
