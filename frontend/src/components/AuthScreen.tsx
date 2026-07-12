import { Check, Eye, EyeOff, LockKeyhole, LogIn, ShieldCheck, UserRound } from "lucide-react";
import { useState, type FormEvent } from "react";
import { SetupRail } from "./SetupRail";
import { BrandLogo } from "./BrandLogo";

type AuthScreenProps = {
  mode: "setup" | "login";
  onSubmit: (username: string, password: string) => Promise<void>;
  error: string;
};

export function AuthScreen({ mode, onSubmit, error }: AuthScreenProps) {
  const [username, setUsername] = useState(mode === "setup" ? "admin" : "");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [visible, setVisible] = useState(false);
  const [busy, setBusy] = useState(false);
  const [localError, setLocalError] = useState("");
  const passwordLongEnough = password.length >= 8;
  const passwordsMatch = confirmation.length > 0 && password === confirmation;

  async function submit(event: FormEvent) {
    event.preventDefault();
    setLocalError("");
    if (mode === "setup" && !passwordLongEnough) {
      setLocalError("Use at least 8 characters for the administrator password.");
      return;
    }
    if (mode === "setup" && !passwordsMatch) {
      setLocalError("Passwords do not match.");
      return;
    }
    setBusy(true);
    try {
      await onSubmit(username, password);
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className={`auth-shell ${mode === "setup" ? "setup-auth-shell" : ""}`}>
      {mode === "setup" ? <SetupRail currentStep={0} /> : <LoginRail />}

      <section className="auth-main" aria-labelledby="auth-title">
        <div className="auth-form-wrap">
          <header className="auth-form-heading">
            <span>{mode === "setup" ? "Account setup" : "Administrator access"}</span>
            <h2 id="auth-title">{mode === "setup" ? "Create your administrator" : "Sign in to Faro"}</h2>
            <p>{mode === "setup" ? "Use this account to access Faro from browsers on your network." : "Enter the local administrator credentials for this installation."}</p>
          </header>

          <form className="auth-form" onSubmit={(event) => void submit(event)}>
            <label>
              <span>Username</span>
              <div className="auth-input"><UserRound size={18} /><input autoComplete="username" autoFocus value={username} onChange={(event) => setUsername(event.target.value)} required minLength={3} maxLength={64} /></div>
            </label>
            <label>
              <span>Password</span>
              <div className="auth-input"><LockKeyhole size={18} /><input type={visible ? "text" : "password"} autoComplete={mode === "setup" ? "new-password" : "current-password"} value={password} onChange={(event) => setPassword(event.target.value)} required minLength={mode === "setup" ? 8 : undefined} /><button type="button" onClick={() => setVisible(!visible)} aria-label={visible ? "Hide password" : "Show password"}>{visible ? <EyeOff size={18} /> : <Eye size={18} />}</button></div>
            </label>
            {mode === "setup" && (
              <>
                <label>
                  <span>Confirm password</span>
                  <div className="auth-input"><LockKeyhole size={18} /><input type={visible ? "text" : "password"} autoComplete="new-password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required minLength={8} /></div>
                </label>
                <div className="password-requirements" aria-label="Password requirements">
                  <span className={passwordLongEnough ? "met" : ""}><Check size={13} /> 8 or more characters</span>
                  <span className={passwordsMatch ? "met" : ""}><Check size={13} /> Passwords match</span>
                </div>
              </>
            )}
            {(localError || error) && <div className="auth-error" role="alert">{localError || error}</div>}
            <button className="auth-submit" type="submit" disabled={busy}>
              {mode === "setup" ? <ShieldCheck size={17} /> : <LogIn size={17} />}
              <span>{busy ? "Please wait" : mode === "setup" ? "Continue to DNS setup" : "Sign in"}</span>
            </button>
          </form>

          <div className="auth-privacy-note"><LockKeyhole size={14} /><span>Credentials are hashed and sessions remain in Faro's local database.</span></div>
        </div>
      </section>
    </main>
  );
}

function LoginRail() {
  return <aside className="auth-context login-rail"><div className="auth-brand"><BrandLogo /><strong className="brand-wordmark">Faro</strong></div><div className="auth-context-copy"><span>Local control plane</span><h1>Your network is waiting.</h1><p>Sign in to return to DNS activity, devices, and filtering controls.</p></div><div className="auth-return-state"><ShieldCheck size={20} /><div><strong>Local session authentication</strong><span>Your Faro data stays behind the administrator account.</span></div></div><footer><ShieldCheck size={15} /><span>Self-hosted and stored locally</span></footer></aside>;
}

export function AuthLoading() {
  return <main className="auth-shell auth-loading-shell"><div className="auth-loading"><BrandLogo /><strong>Faro</strong><span>Checking local session...</span></div></main>;
}
