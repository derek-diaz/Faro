import {
  AlertTriangle,
  BellRing,
  CheckCheck,
  CheckCircle2,
  ChevronRight,
  MonitorSmartphone,
  Network,
  RefreshCcw,
  Shield,
  X
} from "lucide-react";
import type { Page } from "../App";
import type { FaroEvent } from "../api/client";

type NotificationDrawerProps = {
  open: boolean;
  notifications: FaroEvent[];
  attentionCount: number;
  unreadCount: number;
  onClose: () => void;
  onMarkRead: (id: string) => void;
  onDismiss: (id: string) => void;
  onMarkAllRead: () => void;
  onDomainSelect: (domain: string) => void;
  onDeviceSelect: (clientIP: string) => void;
  setPage: (page: Page) => void;
};

export function NotificationDrawer({ open, notifications, attentionCount, unreadCount, onClose, onMarkRead, onDismiss, onMarkAllRead, onDomainSelect, onDeviceSelect, setPage }: NotificationDrawerProps) {
  if (!open) return null;

  const attention = notifications.filter((event) => event.severity === "warning" || event.severity === "critical");
  const recentChanges = notifications.filter((event) => event.severity !== "warning" && event.severity !== "critical");

  function openEvent(event: FaroEvent) {
    if (!event.is_read) onMarkRead(event.id);
    if (event.domain) onDomainSelect(event.domain);
    else if (event.client_ip) onDeviceSelect(event.client_ip);
    else {
      const page = notificationPage(event);
      if (page) setPage(page);
    }
    onClose();
  }

  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside className="notification-drawer network-updates-drawer" onClick={(event) => event.stopPropagation()} aria-label="Network updates">
        <header className="network-updates-header">
          <div>
            <span className="notification-mark"><BellRing size={17} /></span>
            <div><strong>Network updates</strong><span>Important changes and DNS issues</span></div>
          </div>
          <div className="notification-header-actions">
            <button className="notification-mark-all" type="button" onClick={onMarkAllRead} disabled={unreadCount === 0}><CheckCheck size={15} /><span>Mark all read</span></button>
            <button className="icon-button" type="button" onClick={onClose} aria-label="Close network updates"><X size={18} /></button>
          </div>
        </header>

        <div className={`notification-health ${attentionCount > 0 ? "attention" : "clear"}`}>
          {attentionCount > 0 ? <AlertTriangle size={19} /> : <CheckCircle2 size={19} />}
          <div>
            <strong>{attentionCount > 0 ? `${attentionCount} ${attentionCount === 1 ? "item needs" : "items need"} attention` : "Nothing needs attention"}</strong>
            <span>{attentionCount > 0 ? "Review the issue below to keep DNS healthy." : "Faro will flag failures and unusual changes here."}</span>
          </div>
        </div>

        <div className="network-updates-content">
          {attention.length > 0 && (
            <NotificationSection title="Needs attention" count={attention.length} events={attention} onOpen={openEvent} onDismiss={onDismiss} />
          )}

          {recentChanges.length > 0 && (
            <NotificationSection title="Recent changes" count={recentChanges.length} events={recentChanges} onOpen={openEvent} onDismiss={onDismiss} />
          )}

          {notifications.length === 0 && (
            <div className="notification-empty"><CheckCircle2 size={23} /><strong>You're all caught up</strong><span>New devices, configuration changes, and DNS issues will appear here.</span></div>
          )}
        </div>
      </aside>
    </div>
  );
}

function NotificationSection({ title, count, events, onOpen, onDismiss }: { title: string; count: number; events: FaroEvent[]; onOpen: (event: FaroEvent) => void; onDismiss: (id: string) => void }) {
  return (
    <section className="notification-section">
      <div className="notification-section-heading"><h3>{title}</h3><span>{count}</span></div>
      <div className="notification-list">
        {events.map((event) => (
          <div key={event.id} className={`notification-item-row ${event.is_read ? "read" : "unread"}`}>
            <button type="button" className={`notification-item ${event.severity}`} onClick={() => onOpen(event)}>
              <span className="notification-event-icon">{notificationIcon(event)}</span>
              <span className="notification-event-copy">
                <span><strong>{notificationTitle(event)}</strong><time dateTime={event.timestamp}>{relativeTime(event.timestamp)}</time></span>
                <small>{event.description || event.source}</small>
                <em>{notificationTypeLabel(event)}</em>
              </span>
              <ChevronRight className="notification-chevron" size={17} />
            </button>
            <button className="notification-dismiss" type="button" onClick={() => onDismiss(event.id)} aria-label={`Dismiss ${notificationTitle(event)}`} title="Dismiss"><X size={14} /></button>
          </div>
        ))}
      </div>
    </section>
  );
}

function notificationIcon(event: FaroEvent) {
  if (event.severity === "warning" || event.severity === "critical") return <AlertTriangle size={17} />;
  switch (event.type) {
    case "device.first_seen": return <MonitorSmartphone size={17} />;
    case "blocklist.updated":
    case "blocklist.installed": return <Shield size={17} />;
    case "upstream.changed": return <Network size={17} />;
    default: return <RefreshCcw size={17} />;
  }
}

function notificationTitle(event: FaroEvent) {
  switch (event.type) {
    case "device.first_seen": return "New device discovered";
    case "dns.reload_failed": return "DNS reload failed";
    case "blocklist.updated": return "Blocklist refreshed";
    case "blocklist.installed": return "Blocklist installed";
    case "upstream.changed": return "Upstream DNS changed";
    default: return event.title;
  }
}

function notificationTypeLabel(event: FaroEvent) {
  switch (event.type) {
    case "device.first_seen": return "View device";
    case "dns.reload_failed": return "Review DNS settings";
    case "blocklist.updated":
    case "blocklist.installed": return "View blocklists";
    case "upstream.changed": return "View upstreams";
    default: return event.domain ? "Inspect domain" : "Review update";
  }
}

function notificationPage(event: FaroEvent): Page | null {
  switch (event.type) {
    case "dns.reload_failed": return "settings";
    case "blocklist.updated":
    case "blocklist.installed": return "blocklists";
    case "upstream.changed": return "upstreams";
    default: return null;
  }
}

function relativeTime(timestamp: string) {
  const elapsed = Math.max(0, Date.now() - new Date(timestamp).getTime());
  const minutes = Math.floor(elapsed / 60000);
  if (minutes < 1) return "Just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 7) return `${days}d ago`;
  return new Date(timestamp).toLocaleDateString([], { month: "short", day: "numeric" });
}
