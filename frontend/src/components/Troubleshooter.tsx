import { useEffect, useRef, useState } from "react";
import { RefreshCw, ShieldCheck } from "lucide-react";
import { api, type TroubleshootingReport, type TroubleshootingTrial } from "../api/client";

export function Troubleshooter({ clientIP, deviceName, onDomainSelect }: {
  readonly clientIP: string;
  readonly deviceName: string;
  readonly onDomainSelect: (domain: string) => void;
}) {
  const [since, setSince] = useState(() => new Date(Date.now() - 15 * 60_000).toISOString());
  const [report, setReport] = useState<TroubleshootingReport | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [revision, setRevision] = useState(0);
  const [now, setNow] = useState(Date.now());
  const mounted = useRef(true);

  useEffect(() => {
    mounted.current = true;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => { mounted.current = false; window.clearInterval(timer); };
  }, []);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    api.troubleshooting(clientIP, since).then((data) => {
      if (cancelled) return;
      setReport(data);
      setNow(Date.now());
      setSelected((current) => current.filter((domain) => data.items.some((item) => item.domain === domain && item.decision.action === "blocked")));
    }).catch((failure: unknown) => {
      if (!cancelled) setError(failure instanceof Error ? failure.message : "Could not load the investigation.");
    }).finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [clientIP, since, revision]);

  async function change(action: "test" | "keep" | "undo", token?: string) {
    if (!report || busy) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await api.changeTroubleshooting({ action, token, client_ip: clientIP, device_id: report.device_id, protection_id: report.protection_id, domains: selected });
      if (!mounted.current) return;
      setSelected([]);
      setNotice(action === "test" ? "Test started. Retry the site on this device, then keep the exceptions if they helped or undo them." : action === "keep" ? "Exceptions saved in the test's protection." : "Temporary exceptions removed. Existing permanent rules are unchanged.");
      setRevision((value) => value + 1);
    } catch (failure) {
      if (mounted.current) setError(failure instanceof Error ? failure.message : "Could not apply the change.");
    } finally { if (mounted.current) setBusy(false); }
  }

  const trials = new Map<string, TroubleshootingTrial[]>();
  for (const entry of report?.trials ?? []) trials.set(entry.token, [...(trials.get(entry.token) ?? []), entry]);
  const candidates = report?.items.filter((item) => item.blocked > 0 || item.failed > 0) ?? [];

  return <div className="troubleshooter">
    <section className="troubleshooting-intro">
      <ShieldCheck size={24} />
      <div><h3>Fix a broken site</h3><p>Find out whether Faro is blocking something {deviceName} needs.</p></div>
    </section>
    <section className="troubleshooting-step">
      <h3>1. Reproduce the problem</h3>
      <p>Start a fresh capture, open the broken site or app on this device, then refresh the results. Only requests reaching this Faro appear here.</p>
      <div className="troubleshooting-actions">
        <button type="button" disabled={busy || loading} onClick={() => { setSelected([]); setNotice(""); setReport(null); setSince(new Date().toISOString()); }}>Start fresh capture</button>
        <button type="button" className="secondary" disabled={busy || loading} onClick={() => setRevision((value) => value + 1)}><RefreshCw size={15} /> Refresh results</button>
      </div>
      <small>Requests since {new Date(since).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}. Capture reads existing DNS logs.</small>
    </section>
    {error && <p className="troubleshooting-error" role="alert">{error}</p>}
    {notice && <p className="troubleshooting-notice" role="status">{notice}</p>}
    {loading && <p role="status">Loading requests…</p>}
    {report && <>
      <section className="troubleshooting-step">
        <h3>2. Test likely causes</h3>
        <p>Blocked requests are clues, not proof of the problem. Select only domains you recognize as relevant. Failed DNS responses can also have an upstream cause.</p>
        <p className="troubleshooting-scope"><strong>Scope: {report.protection_name}.</strong> A test allows the selected exact domains for 10 minutes for <strong>every device using this protection</strong>. It does not allow subdomains automatically.</p>
        {!report.temporary_tests_available && <p className="troubleshooting-scope">Temporary tests are available on standalone Faro only. A disconnected replica cannot guarantee expiry. You can inspect these results and make deliberate changes in Protection.</p>}
        {candidates.length === 0 ? <p className="troubleshooting-empty">{report.items.length ? "No blocked requests or DNS failures in this capture. Check the device's DNS settings, connection, or the site itself." : "No requests captured yet. Retry the site on this device, wait a few seconds, and refresh."}</p> : <div className="troubleshooting-candidates">
          {candidates.map((item) => <label className="troubleshooting-candidate" key={item.domain}>
            <input type="checkbox" aria-label={`Test ${item.domain}`} checked={selected.includes(item.domain)} disabled={busy || loading || !report.temporary_tests_available || item.decision.action !== "blocked" || (!selected.includes(item.domain) && selected.length >= 20)} onChange={(event) => setSelected((current) => event.target.checked ? [...current, item.domain] : current.filter((domain) => domain !== item.domain))} />
            <span><button className="table-link" type="button" onClick={(event) => { event.preventDefault(); onDomainSelect(item.domain); }}>{item.domain}</button><small>{item.blocked} blocked · {item.failed} failed · {item.requests} requests</small><small>Current policy: {item.decision.reason}</small></span>
          </label>)}
        </div>}
        {report.truncated && <p>Showing the 100 highest-priority domains. Start a fresh capture to narrow the results.</p>}
        <div className="troubleshooting-actions"><button type="button" disabled={busy || loading || Boolean(error) || !report.temporary_tests_available || selected.length === 0} onClick={() => void change("test")}>Allow {selected.length || "selected"} temporarily</button><small>Up to 20 domains per test</small></div>
      </section>
      <section className="troubleshooting-step">
        <h3>3. Retry, then keep or undo</h3>
        <p>Retry the site after starting a test. Closing this panel leaves the test running. Faro expires it automatically; DNS and app caches may take longer to reflect a change.</p>
        {trials.size === 0 && <p className="troubleshooting-empty">Your tests will appear here, including when you reopen this device.</p>}
        {[...trials].map(([token, entries]) => {
          const remaining = Math.max(0, Math.ceil((new Date(entries[0].expires_at).getTime() - now) / 1000));
          return <div className="troubleshooting-trial" key={token}>
            <strong>{entries[0].protection_name} · {remaining ? `${Math.floor(remaining / 60)}:${String(remaining % 60).padStart(2, "0")} remaining` : "Expiry reached"}</strong>
            <p>{entries.map((entry) => entry.domain).join(", ")}</p>
            <small>Keeping these creates permanent exceptions for every device using {entries[0].protection_name} and replaces conflicting custom blocks.</small>
            <div className="troubleshooting-actions"><button type="button" disabled={busy || loading || !report.temporary_tests_available} onClick={() => void change("keep", token)}>It helped — keep exceptions</button><button className="secondary" type="button" disabled={busy || loading || !report.temporary_tests_available} onClick={() => void change("undo", token)}>Undo test</button></div>
          </div>;
        })}
      </section>
    </>}
  </div>;
}
