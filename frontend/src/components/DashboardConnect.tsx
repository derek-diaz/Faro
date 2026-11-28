import { useEffect, useRef, useState } from "react";
import { api, type DNSQuery, type Setting } from "../api/client";
import { errorMessage } from "../utils/formatting";
import { normalizeLANAddress, recentClientQueries } from "../utils/onboarding";
import { DnsServerAddress } from "./DnsServerAddress";
import { Button } from "./ui/button";
import "./dashboard-connect.css";

type DashboardConnectProps = {
  readonly settings: Setting[];
  readonly onConnected: (query: DNSQuery) => void;
  readonly onOpenSettings: () => void;
};

export function DashboardConnect({ settings, onConnected, onOpenSettings }: DashboardConnectProps) {
  const address = normalizeLANAddress(settings.find((setting) => setting.key === "faro_lan_ip")?.value ?? "");

  return <section className="dashboard-connect-guide" id="dashboard-connect-guide" aria-labelledby="dashboard-connect-heading">
    <h3 id="dashboard-connect-heading">Try one device on Wi-Fi</h3>
    {address ? <DeviceConnection key={address} address={address} onConnected={onConnected} /> : <>
      <p>First, save the fixed local IP of the computer running Faro in the <strong>Faro LAN address</strong> field. Then return here to connect a device.</p>
      <a href="/settings#general" onClick={(event) => {
        if (event.button || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
        event.preventDefault();
        onOpenSettings();
      }}>Open DNS settings</a>
    </>}
  </section>;
}

type ConnectionCheck =
  | { state: "idle" | "checking" | "empty" | "canceled" }
  | { state: "error"; message: string }
  | { state: "received"; query: DNSQuery };

function DeviceConnection({ address, onConnected }: { readonly address: string; readonly onConnected: (query: DNSQuery) => void }) {
  const [openedAt] = useState(Date.now);
  const [check, setCheck] = useState<ConnectionCheck>({ state: "idle" });
  const request = useRef<AbortController | null>(null);

  useEffect(() => () => request.current?.abort(), []);

  async function checkConnection() {
    if (request.current) {
      request.current.abort();
      request.current = null;
      setCheck({ state: "canceled" });
      return;
    }
    const controller = new AbortController();
    request.current = controller;
    setCheck({ state: "checking" });
    try {
      const queries = await api.queries("", controller.signal);
      if (controller.signal.aborted) return;
      const [latest] = recentClientQueries(queries, address, openedAt);
      if (latest) {
        setCheck({ state: "received", query: latest });
        onConnected(latest);
      } else {
        setCheck({ state: "empty" });
      }
    } catch (error) {
      if (!controller.signal.aborted) setCheck({ state: "error", message: errorMessage(error, "Try checking again in a moment.") });
    } finally {
      if (request.current === controller) request.current = null;
    }
  }

  return <>
    <DnsServerAddress address={address} />
    <ol>
      <li>On your phone, join the same network as Faro. In Wi-Fi settings, open the details for your current network.</li>
      <li>Find its DNS settings, choose manual DNS and enter the address above as the only DNS server. Save and reconnect to Wi-Fi.</li>
      <li>Open a website you haven’t visited recently on that phone, then check here for its request.</li>
    </ol>
    <Button type="button" variant={check.state === "checking" || check.state === "received" ? "outline" : "default"} onClick={() => void checkConnection()}>
      {check.state === "checking" ? "Cancel check" : "Check for DNS activity"}
    </Button>
    <div className="dashboard-connect-feedback" role="status" aria-atomic="true">
      {check.state === "checking" && <p>Checking recent DNS requests…</p>}
      {check.state === "canceled" && <p>Check canceled.</p>}
      {check.state === "error" && <p><strong>Couldn’t check DNS activity.</strong> {check.message}</p>}
      {check.state === "empty" && <p><strong>No new device request seen yet.</strong> Check the DNS address, open another website and try again in a few seconds. A VPN or private DNS may send requests elsewhere.</p>}
      {check.state === "received" && <p><strong>DNS request received.</strong> Faro saw <bdi>{check.query.domain}</bdi> from <bdi>{check.query.client_ip}</bdi>. Compare this address with the Wi-Fi IP on your phone.</p>}
    </div>
  </>;
}
