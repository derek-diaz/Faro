import { Car, Edit3, HardDrive, Laptop, MonitorSmartphone, Server, Smartphone, Tv } from "lucide-react";
import { useEffect, useState } from "react";
import { api, type DeviceSummary } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";

type DevicesProps = {
  devices: DeviceSummary[];
  refresh: () => Promise<void>;
  selectedClientIP: string | null;
  onSelectClient: (clientIP: string | null) => void;
  onDomainSelect: (domain: string) => void;
};

export function Devices({ devices, refresh, selectedClientIP, onSelectClient, onDomainSelect }: DevicesProps) {
  const [detail, setDetail] = useState<DeviceSummary | null>(null);
  const [form, setForm] = useState({ name: "", location: "", notes: "" });
  const [editing, setEditing] = useState(false);

  useEffect(() => {
    if (!selectedClientIP) {
      setDetail(null);
      setEditing(false);
      return;
    }
    let cancelled = false;
    api.device(selectedClientIP).then((nextDetail) => {
      if (!cancelled) {
        setDetail(nextDetail);
        setForm({ name: nextDetail.name || "", location: nextDetail.location ?? "", notes: nextDetail.notes ?? "" });
      }
    });
    return () => {
      cancelled = true;
    };
  }, [selectedClientIP]);

  async function saveAlias(event: React.FormEvent) {
    event.preventDefault();
    if (!selectedClientIP) {
      return;
    }
    await api.updateDeviceAlias(selectedClientIP, form);
    setEditing(false);
    await refresh();
    setDetail(await api.device(selectedClientIP));
  }

  if (devices.length === 0) {
    return (
      <EmptyState
        title="No devices yet"
        body="Point a device or router at Faro to start seeing clients, names, blocked requests, and top domains."
      />
    );
  }

  return (
    <div className="devices-layout">
      <section className="panel">
        <div className="panel-title">
          <h2>Devices</h2>
        </div>
        <div className="device-list">
          {devices.map((device) => (
            <button
              className={selectedClientIP === device.client_ip ? "device-card active" : "device-card"}
              key={device.client_ip}
              type="button"
              onClick={() => onSelectClient(device.client_ip)}
            >
              <span className="device-icon">
                {deviceTypeIcon(device.device_type)}
              </span>
              <span className="device-main">
                <strong>{device.name || device.client_ip}</strong>
                <small>{device.device_type} · {device.name ? device.client_ip : "Unnamed device"}</small>
              </span>
              <span className="device-stat">
                <strong>{device.total_queries_today}</strong>
                <small>today</small>
              </span>
              <span className="device-stat blocked">
                <strong>{device.block_percentage.toFixed(1)}%</strong>
                <small>blocked</small>
              </span>
            </button>
          ))}
        </div>
      </section>

      <section className="panel device-detail-panel">
        {!detail ? (
          <EmptyState title="Pick a device" body="Select a client to see its top domains, recent activity, and friendly name." />
        ) : (
          <>
            <div className="device-detail-header">
              <div>
                <span>Assigned profile: {detail.profile}</span>
                <h2>{detail.name || detail.client_ip}</h2>
                <p>{detail.device_type} · {detail.location || (detail.name ? detail.client_ip : "Add a friendly name so this device is easier to recognize.")}</p>
              </div>
              <button className="secondary" type="button" onClick={() => setEditing((value) => !value)}>
                <Edit3 size={16} />
                <span>{editing ? "Cancel" : "Edit name"}</span>
              </button>
            </div>

            {editing && (
              <form className="alias-form" onSubmit={(event) => void saveAlias(event)}>
                <label>
                  Friendly name
                  <input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder="Living room TV" />
                </label>
                <label>
                  Location
                  <input value={form.location} onChange={(event) => setForm({ ...form, location: event.target.value })} placeholder="Living room" />
                </label>
                <label>
                  Notes
                  <input value={form.notes} onChange={(event) => setForm({ ...form, notes: event.target.value })} placeholder="Optional notes" />
                </label>
                <button type="submit">Save device</button>
              </form>
            )}

            <div className="detail-grid device-detail-grid">
              <Detail label="Queries today" value={detail.total_queries_today} />
              <Detail label="Blocked today" value={detail.blocked_queries_today} />
              <Detail label="Block rate" value={`${detail.block_percentage.toFixed(1)}%`} />
              <Detail label="Last seen" value={detail.last_seen ? new Date(detail.last_seen).toLocaleString() : "Not seen yet"} />
            </div>

            <div className="device-detail-columns">
              <section>
                <h3>Top domains</h3>
                {detail.top_domains.length === 0 ? (
                  <p className="empty">No domains for this device yet.</p>
                ) : (
                  <div className="mini-list">
                    {detail.top_domains.map((domain) => (
                      <button className="mini-list-button" type="button" key={domain.label} onClick={() => onDomainSelect(domain.label)}>
                        <span>{domain.label}</span>
                        <strong>{domain.count}</strong>
                      </button>
                    ))}
                  </div>
                )}
              </section>
              <section>
                <h3>Recent activity</h3>
                {detail.recent_activity?.length ? (
                  <div className="device-activity-list">
                    {detail.recent_activity.map((query) => (
                      <div key={`${query.id ?? query.timestamp}-${query.domain}-${query.query_type}`}>
                        <StatusBadge value={query.action} />
                        <button className="link-button" type="button" onClick={() => onDomainSelect(query.domain)}>
                          {query.domain}
                        </button>
                        <small>{new Date(query.timestamp).toLocaleTimeString()}</small>
                      </div>
                    ))}
                  </div>
                ) : (
                  <p className="empty">No recent activity for this device.</p>
                )}
              </section>
            </div>
          </>
        )}
      </section>
    </div>
  );
}

function deviceTypeIcon(type: string) {
  switch (type) {
    case "Apple TV":
      return <Tv size={20} />;
    case "Tesla":
      return <Car size={20} />;
    case "Windows PC":
    case "Mac":
      return <Laptop size={20} />;
    case "Linux Server":
      return <Server size={20} />;
    case "NAS":
      return <HardDrive size={20} />;
    case "Android Phone":
      return <Smartphone size={20} />;
    default:
      return <MonitorSmartphone size={20} />;
  }
}

function Detail({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="detail-item">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}
