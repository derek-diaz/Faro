import { lazy, Suspense, useEffect, useState } from "react";
import { api, type AuthStatus, type RedundancyPublicStatus } from "./api/client";
import { AuthLoading, AuthScreen } from "./components/AuthScreen";
import { applyThemeMode, persistThemeMode, readThemeMode, type ThemeMode } from "./theme";

export type { Page } from "./AuthenticatedApp";

const AuthenticatedApp = lazy(() => import("./AuthenticatedApp"));
const Onboarding = lazy(() => import("./components/Onboarding").then((module) => ({ default: module.Onboarding })));
const ReplicaNodeScreen = lazy(() => import("./components/RedundancySetup").then((module) => ({ default: module.ReplicaNodeScreen })));
const JoinExistingFaro = lazy(() => import("./components/RedundancySetup").then((module) => ({ default: module.JoinExistingFaro })));

export function App() {
  const [themeMode, setThemeMode] = useState<ThemeMode>(() => readThemeMode());

  useEffect(() => {
    applyThemeMode(themeMode);
    persistThemeMode(themeMode);
    if (themeMode !== "system") return undefined;
    const preference = window.matchMedia("(prefers-color-scheme: dark)");
    const updateResolvedTheme = () => applyThemeMode(themeMode);
    preference.addEventListener("change", updateResolvedTheme);
    return () => preference.removeEventListener("change", updateResolvedTheme);
  }, [themeMode]);

  const appearance = { themeMode, onThemeModeChange: setThemeMode };
  return (
    <Suspense fallback={<AuthLoading {...appearance} />}>
      <AppGate {...appearance} />
    </Suspense>
  );
}

function AppGate(appearanceProps: { themeMode: ThemeMode; onThemeModeChange: (mode: ThemeMode) => void }) {
  const [auth, setAuth] = useState<AuthStatus | null>(null);
  const [redundancy, setRedundancy] = useState<RedundancyPublicStatus | null>(null);
  const [joiningExisting, setJoiningExisting] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [authError, setAuthError] = useState("");

  useEffect(() => {
    let active = true;
    Promise.all([
      api.authStatus(),
      api.redundancyPublic()
    ])
      .then(([status, redundancyStatus]) => { if (active) { setAuth(status); setRedundancy(redundancyStatus); } })
      .catch((error_) => { if (active) setAuthError(error_ instanceof Error ? error_.message : "Faro API is unavailable."); });
    function unauthorized() {
      setAuth((current) => current ? { ...current, authenticated: false, username: undefined } : current);
    }
    window.addEventListener("faro:unauthorized", unauthorized);
    return () => {
      active = false;
      window.removeEventListener("faro:unauthorized", unauthorized);
    };
  }, [attempt]);

  async function authenticate(username: string, password: string) {
    setAuthError("");
    try {
      if (auth?.configured) {
        await api.login(username, password);
      } else {
        await api.setupAuth(username, password);
      }
      setAuth(await api.authStatus());
    } catch (error_) {
      setAuthError(error_ instanceof Error ? error_.message : "Authentication failed.");
    }
  }

  const authProps = { onSubmit: authenticate, error: authError, ...appearanceProps };
  const startJoining = () => setJoiningExisting(true);
  const cancelJoining = () => setJoiningExisting(false);

  if (!auth || !redundancy) {
    function retry() {
      setAuthError("");
      setAttempt((current) => current + 1);
    }
    return <AuthLoading {...appearanceProps} onRetry={authError ? retry : undefined} />;
  }
  if (redundancy.role === "replica") {
    return (
      <ReplicaNodeScreen
        initialStatus={redundancy}
        configured={auth.configured}
        authenticated={auth.authenticated}
        username={auth.username}
        {...appearanceProps}
        onLeft={(next) => {
          void api.authStatus()
            .then((nextAuth) => {
              setAuth(nextAuth);
              setRedundancy(next);
            })
            .catch(() => setRedundancy(next));
        }}
      />
    );
  }
  if (joiningExisting) {
    if (auth.configured && !auth.authenticated) {
      return <AuthScreen mode="login" onCancelJoin={cancelJoining} {...authProps} />;
    }
    return <JoinExistingFaro onBack={cancelJoining} onJoined={setRedundancy} {...appearanceProps} />;
  }
  if (!auth.configured || !auth.authenticated) {
    return <AuthScreen mode={auth.configured ? "login" : "setup"} onJoinExisting={startJoining} {...authProps} />;
  }
  if (!auth.onboarding_complete) {
    return (
      <Onboarding
        username={auth.username || "admin"}
        onComplete={() => setAuth({ ...auth, onboarding_complete: true })}
        onJoinExisting={startJoining}
        {...appearanceProps}
      />
    );
  }
  return (
    <AuthenticatedApp
      username={auth.username || "admin"}
      onSignedOut={() => setAuth({ configured: true, authenticated: false, onboarding_complete: true })}
      {...appearanceProps}
    />
  );
}
