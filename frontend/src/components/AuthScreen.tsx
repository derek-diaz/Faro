import { Check, Eye, EyeOff, LockKeyhole, LogIn, Network, ShieldCheck, UserRound } from "lucide-react";
import { useState, type SubmitEvent } from "react";
import type { ThemeMode } from "../theme";
import { AppearanceMenu } from "./AppearanceMenu";
import { SetupRail } from "./SetupRail";
import { BrandLogo } from "./BrandLogo";

type AuthScreenProps = {
  readonly mode: "setup" | "login";
  readonly onSubmit: (username: string, password: string) => Promise<void>;
  readonly error: string;
  readonly onJoinExisting?: () => void;
  readonly themeMode: ThemeMode;
  readonly onThemeModeChange: (mode: ThemeMode) => void;
};

export function AuthScreen({ mode, onSubmit, error, onJoinExisting, themeMode, onThemeModeChange }: AuthScreenProps) {
  const isSetup = mode === "setup";
  const [username, setUsername] = useState(mode === "setup" ? "admin" : "");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [visible, setVisible] = useState(false);
  const [busy, setBusy] = useState(false);
  const [localError, setLocalError] = useState("");
  const passwordLongEnough = password.length >= 8;
  const passwordsMatch = confirmation.length > 0 && password === confirmation;

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    setLocalError("");
    const validationError = authValidationError(isSetup, passwordLongEnough, passwordsMatch);
    if (validationError) {
      setLocalError(validationError);
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
    <AuthLayout
      mode={mode}
      username={username}
      setUsername={setUsername}
      password={password}
      setPassword={setPassword}
      confirmation={confirmation}
      setConfirmation={setConfirmation}
      visible={visible}
      setVisible={setVisible}
      busy={busy}
      error={localError || error}
      passwordLongEnough={passwordLongEnough}
      passwordsMatch={passwordsMatch}
      onSubmit={submit}
      onJoinExisting={onJoinExisting}
      themeMode={themeMode}
      onThemeModeChange={onThemeModeChange}
    />
  );
}

type AuthLayoutProps = {
  readonly mode: AuthScreenProps["mode"];
  readonly username: string;
  readonly setUsername: (value: string) => void;
  readonly password: string;
  readonly setPassword: (value: string) => void;
  readonly confirmation: string;
  readonly setConfirmation: (value: string) => void;
  readonly visible: boolean;
  readonly setVisible: (value: boolean) => void;
  readonly busy: boolean;
  readonly error: string;
  readonly passwordLongEnough: boolean;
  readonly passwordsMatch: boolean;
  readonly onSubmit: (event: SubmitEvent) => Promise<void>;
  readonly onJoinExisting?: () => void;
  readonly themeMode: ThemeMode;
  readonly onThemeModeChange: (mode: ThemeMode) => void;
};

function AuthLayout({ mode, onJoinExisting, themeMode, onThemeModeChange, ...formProps }: AuthLayoutProps) {
  const isSetup = mode === "setup";
  return (
    <main className={`auth-shell ${isSetup ? "setup-auth-shell" : ""}`}>
      <AppearanceMenu className="public-appearance-control" themeMode={themeMode} onThemeModeChange={onThemeModeChange} />
      {isSetup ? <SetupRail currentStep={0} /> : <LoginRail />}
      <section className="auth-main" aria-labelledby="auth-title">
        <div className="auth-form-wrap">
          <AuthForm mode={mode} {...formProps} />
          {onJoinExisting && <JoinExisting onJoinExisting={onJoinExisting} />}
        </div>
      </section>
    </main>
  );
}

function AuthForm({ mode, username, setUsername, password, setPassword, confirmation, setConfirmation, visible, setVisible, busy, error, passwordLongEnough, passwordsMatch, onSubmit }: Omit<AuthLayoutProps, "onJoinExisting" | "themeMode" | "onThemeModeChange">) {
  return (
    <>
      <AuthHeader mode={mode} />
      <form className="auth-form" onSubmit={(event) => void onSubmit(event)}>
        <label>
          <span>Username</span>
          <div className="auth-input"><UserRound size={18} /><input autoComplete="username" autoFocus value={username} onChange={(event) => setUsername(event.target.value)} required minLength={3} maxLength={64} /></div>
        </label>
        <PasswordField mode={mode} password={password} setPassword={setPassword} visible={visible} setVisible={setVisible} />
        {mode === "setup" && <SetupPasswordFields confirmation={confirmation} setConfirmation={setConfirmation} visible={visible} passwordLongEnough={passwordLongEnough} passwordsMatch={passwordsMatch} />}
        {error && <div className="auth-error" role="alert">{error}</div>}
        <SubmitButton mode={mode} busy={busy} />
      </form>
    </>
  );
}

function AuthHeader({ mode }: Readonly<Pick<AuthLayoutProps, "mode">>) {
  const copy = authCopy(mode);
  return (
    <header className="auth-form-heading">
      <span>{copy.label}</span>
      <h2 id="auth-title">{copy.title}</h2>
      <p>{copy.description}</p>
    </header>
  );
}

function authCopy(mode: AuthScreenProps["mode"]) {
  if (mode === "setup") {
    return {
      label: "Account setup",
      title: "Create your administrator",
      description: "Use this account to access Faro from browsers on your network."
    };
  }
  return { label: "Welcome back", title: "Sign in to Faro", description: "Use your administrator account to continue." };
}

type PasswordFieldProps = {
  readonly mode: AuthScreenProps["mode"];
  readonly password: string;
  readonly setPassword: (value: string) => void;
  readonly visible: boolean;
  readonly setVisible: (value: boolean) => void;
};

function PasswordField({ mode, password, setPassword, visible, setVisible }: PasswordFieldProps) {
  const inputType = visible ? "text" : "password";
  const visibilityLabel = visible ? "Hide password" : "Show password";
  return (
    <label>
      <span>Password</span>
      <div className="auth-input"><LockKeyhole size={18} /><input type={inputType} autoComplete={mode === "setup" ? "new-password" : "current-password"} value={password} onChange={(event) => setPassword(event.target.value)} required minLength={mode === "setup" ? 8 : undefined} /><button type="button" onClick={() => setVisible(!visible)} aria-label={visibilityLabel}>{visible ? <EyeOff size={18} /> : <Eye size={18} />}</button></div>
    </label>
  );
}

type SetupPasswordFieldsProps = {
  readonly confirmation: string;
  readonly setConfirmation: (value: string) => void;
  readonly visible: boolean;
  readonly passwordLongEnough: boolean;
  readonly passwordsMatch: boolean;
};

function SetupPasswordFields({ confirmation, setConfirmation, visible, passwordLongEnough, passwordsMatch }: SetupPasswordFieldsProps) {
  const inputType = visible ? "text" : "password";
  return (
    <>
      <label>
        <span>Confirm password</span>
        <div className="auth-input"><LockKeyhole size={18} /><input type={inputType} autoComplete="new-password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required minLength={8} /></div>
      </label>
      <div className="password-requirements" aria-label="Password requirements">
        <span className={passwordLongEnough ? "met" : ""}><Check size={13} /> 8 or more characters</span>
        <span className={passwordsMatch ? "met" : ""}><Check size={13} /> Passwords match</span>
      </div>
    </>
  );
}

function SubmitButton({ mode, busy }: Readonly<Pick<AuthLayoutProps, "mode" | "busy">>) {
  return (
    <button className="auth-submit" type="submit" disabled={busy}>
      {mode === "setup" ? <ShieldCheck size={17} /> : <LogIn size={17} />}
      <span>{authSubmitLabel(mode, busy)}</span>
    </button>
  );
}

function JoinExisting({ onJoinExisting }: Readonly<Pick<AuthLayoutProps, "onJoinExisting">>) {
  return (
    <div className="auth-join-existing">
      <span>Already have Faro running elsewhere?</span>
      <button type="button" className="secondary" onClick={onJoinExisting}><Network size={16} />Join an existing Faro home</button>
    </div>
  );
}

function authValidationError(isSetup: boolean, passwordLongEnough: boolean, passwordsMatch: boolean): string {
  if (!isSetup) {
    return "";
  }
  if (!passwordLongEnough) {
    return "Use at least 8 characters for the administrator password.";
  }
  if (!passwordsMatch) {
    return "Passwords do not match.";
  }
  return "";
}

function authSubmitLabel(mode: AuthScreenProps["mode"], busy: boolean): string {
  if (busy) {
    return "Please wait";
  }
  if (mode === "setup") {
    return "Continue to DNS setup";
  }
  return "Sign in";
}

function LoginRail() {
  return (
    <aside className="auth-context login-rail">
      <div className="auth-brand"><BrandLogo /><strong className="brand-wordmark">Faro</strong></div>
      <div className="auth-context-copy">
        <span>Network control</span>
        <h1>Your network, at a glance.</h1>
        <p>Manage DNS activity, devices, and protection from one place.</p>
      </div>
    </aside>
  );
}

export function AuthLoading({ themeMode, onThemeModeChange }: Pick<AuthScreenProps, "themeMode" | "onThemeModeChange">) {
  return <main className="auth-shell auth-loading-shell"><AppearanceMenu className="public-appearance-control" themeMode={themeMode} onThemeModeChange={onThemeModeChange} /><div className="auth-loading"><BrandLogo /><strong>Faro</strong><span>Checking local session...</span></div></main>;
}
