import { Check } from "lucide-react";
import { BrandLogo } from "./BrandLogo";

export const setupStages = [
  { label: "Administrator", detail: "Protect access to Faro" },
  { label: "Local network", detail: "Names and caching" },
  { label: "Upstreams", detail: "Public DNS providers" },
  { label: "Protection", detail: "Optional filtering" },
  { label: "Connect", detail: "Review and finish" }
];

export function SetupRail({ currentStep, username }: { currentStep: number; username?: string }) {
  return (
    <aside className="onboarding-rail setup-rail">
      <div className="onboarding-brand"><BrandLogo /><div><strong className="brand-wordmark">Faro</strong><span>Initial setup</span></div></div>
      <ol>
        {setupStages.map((stage, index) => (
          <li key={stage.label} className={index === currentStep ? "active" : index < currentStep ? "complete" : ""}>
            <span>{index < currentStep ? <Check size={14} /> : index + 1}</span>
            <div><strong>{stage.label}</strong><small>{stage.detail}</small></div>
          </li>
        ))}
      </ol>
      {username ? (
        <div className="onboarding-user"><span>{username.slice(0, 1).toUpperCase()}</span><div><small>Administrator</small><strong>{username}</strong></div></div>
      ) : (
        <div className="setup-rail-note"><span>1</span><div><small>Current step</small><strong>Create administrator</strong></div></div>
      )}
    </aside>
  );
}
