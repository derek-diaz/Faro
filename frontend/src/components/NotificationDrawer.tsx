import { Bell, X } from "lucide-react";
import type { FaroEvent } from "../api/client";

type NotificationDrawerProps = {
  open: boolean;
  notifications: FaroEvent[];
  onClose: () => void;
  onDomainSelect: (domain: string) => void;
  onDeviceSelect: (clientIP: string) => void;
};

export function NotificationDrawer({ open, notifications, onClose, onDomainSelect, onDeviceSelect }: NotificationDrawerProps) {
  if (!open) {
    return null;
  }

  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside className="notification-drawer" onClick={(event) => event.stopPropagation()} aria-label="Notifications">
        <div className="drawer-header">
          <div className="drawer-domain-title">
            <span className="notification-mark">
              <Bell size={18} />
            </span>
            <div>
              <strong>Notifications</strong>
              <span>{notifications.length} recent network events</span>
            </div>
          </div>
          <button className="icon-button" type="button" onClick={onClose} aria-label="Close notifications">
            <X size={18} />
          </button>
        </div>

        <div className="notification-list">
          {notifications.length === 0 ? (
            <p className="empty">No notifications yet.</p>
          ) : (
            notifications.map((event) => (
              <button
                key={event.id}
                type="button"
                className={`notification-item ${event.severity}`}
                onClick={() => {
                  if (event.domain) {
                    onDomainSelect(event.domain);
                  }
                  if (event.client_ip && !event.domain) {
                    onDeviceSelect(event.client_ip);
                  }
                  onClose();
                }}
              >
                <span>{notificationTitle(event)}</span>
                <small>{event.description || event.source}</small>
                <time>{new Date(event.timestamp).toLocaleTimeString()}</time>
              </button>
            ))
          )}
        </div>
      </aside>
    </div>
  );
}

function notificationTitle(event: FaroEvent) {
  switch (event.type) {
    case "device.first_seen":
      return "New device discovered";
    case "dns.reload":
      return "DNS successfully reloaded";
    case "dns.reload_failed":
      return "DNS reload failed";
    case "blocklist.updated":
      return "Blocklist updated";
    case "blocklist.installed":
      return "Blocklist installed";
    case "upstream.changed":
      return "New upstream configured";
    default:
      return event.title;
  }
}
