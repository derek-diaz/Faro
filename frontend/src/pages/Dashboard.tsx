import { Table } from "../components/ui/table";
import { Button } from "../components/ui/button";
import { AlertTriangle, ArrowRight, ChevronRight, Database, Globe2, LifeBuoy, ListFilter, MonitorSmartphone, RadioTower } from "lucide-react";
import { tableFeatures, useTable } from "@tanstack/react-table";
import type { ColumnDef } from "@tanstack/react-table";
import { useMemo, useRef, useState } from "react";
import type { CountItem, DashboardSummary, DNSQuery, Setting, WhatsNewItem } from "../api/client";
import { DomainFavicon } from "@/components/DomainFavicon";
import { LoadingState } from "@/components/LoadingState";
import { ResolutionSource } from "@/components/ResolutionSource";
import { Badge } from "@/components/ui/badge";
import { formatDate, formatTime } from "@/utils/dateFormatting";
import { formatNumber } from "@/utils/formatting";

import { DashboardConnect } from "../components/DashboardConnect";
import { DashboardProviders } from "../components/DashboardProviders";
import { DashboardTraffic } from "../components/DashboardTraffic";
import type { dashboardStatus } from "../utils/dashboardStatus";
import "./dashboard-home.css";

type DashboardProps = {
  summary: DashboardSummary | null;
  settings: Setting[];
  loading: boolean;
  status: ReturnType<typeof dashboardStatus>;
  onDomainSelect: (domain: string) => void;
  onDeviceSelect: (reference: string) => void;
  onViewActivity: () => void;
  onViewHour: (from: string, to: string) => void;
  onViewServerHealth: () => void;
  onViewDnsHealth: () => void;
  onConnectionSettings: () => void;
  onConnected: () => void;
  onTroubleshoot: () => void;
  onViewDevices: () => void;
  onViewBlocklists: (id?: number) => void;
  onViewLocalDns: (id?: number) => void;
  onManageUpstreams: () => void;
};

const dashboardActivityFeatures = tableFeatures({});

export function Dashboard(props: DashboardProps) {
  const { summary, status } = props;
  const [connecting, setConnecting] = useState(false);
  const todayHeading = useRef<HTMLHeadingElement>(null);
  if (!summary) {
    return props.loading
      ? <LoadingState className="dashboard-loading" title="Loading dashboard" description="Checking DNS and recent activity…" />
      : <p role="status">The network overview could not be loaded.</p>;
  }

  const replica = summary.stories?.find((item) => item.section === "redundancy");
  const hasDiscoveries = Object.values(summary.whats_new ?? {}).some((items) => items?.length);
  const providers = <DashboardProviders summary={summary} settings={props.settings} onManage={props.onManageUpstreams} />;
  const healthActions = {
    providers: { detail: summary.upstream_health, label: "View DNS providers", open: props.onManageUpstreams },
    health: { detail: summary.health_cards?.find((card) => card.label === "DNS")?.detail || "The DNS engine health check is unavailable.", label: "View DNS health", open: props.onViewDnsHealth },
    activity: { detail: "Recent requests received a server failure response.", label: "View activity", open: props.onViewActivity },
  };
  const issue = status.action && healthActions[status.action];

  function toggleGuide() {
    if (connecting && !status.offerConnection) todayHeading.current?.focus();
    setConnecting(!connecting);
  }
  return (
    <div className="observability-dashboard dashboard-home">
      {issue && <section className="home-attention" aria-label="DNS needs attention">
        <AlertTriangle size={19} aria-hidden="true" />
        <p>{issue.detail}</p>
        <Button variant="link" onClick={issue.open}>{issue.label}<ArrowRight size={15} /></Button>
      </section>}
      {replica && <section className="dashboard-replica-warning" aria-label="Backup server warning">
        <AlertTriangle size={20} aria-hidden="true" />
        <div><strong>{replica.title}</strong><p>{replica.body}</p></div>
        <a href="/settings#redundancy" onClick={(event) => {
          if (event.button || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
          event.preventDefault();
          props.onViewServerHealth();
        }}>View server health <ArrowRight size={15} aria-hidden="true" /></a>
      </section>}

      {(status.offerConnection || connecting) && <section className="panel home-first-device">
        <div className="home-connect-intro">
          <RadioTower size={24} aria-hidden="true" />
          <div><h2>{status.noHistory ? "Start with one device" : "Check a device connection"}</h2><p>Use Faro for DNS on one phone or computer to see its requests here.</p></div>
          <Button variant={connecting ? "outline" : "default"} aria-expanded={connecting} aria-controls={connecting ? "dashboard-connect-guide" : undefined} onClick={toggleGuide}>
            {connecting ? "Close guide" : "Connect a device"}
          </Button>
        </div>
        {connecting && <DashboardConnect settings={props.settings} onOpenSettings={props.onConnectionSettings} onConnected={props.onConnected} />}
      </section>}

      {!status.noHistory &&
        <section className="home-today" aria-labelledby="home-today-title">
          <h2 ref={todayHeading} tabIndex={-1} id="home-today-title">Today’s activity</h2>
          <dl>
            <div><dt>DNS requests</dt><dd>{formatNumber(summary.total_queries_today)}</dd></div>
            <div><dt>Blocked</dt><dd>{formatNumber(summary.blocked_queries_today)} <small>{summary.block_percentage.toFixed(1)}%</small></dd></div>
            <div><dt>Active devices</dt><dd>{formatNumber(summary.active_devices_today)}</dd></div>
          </dl>
        </section>}
      <div className={status.noHistory ? "home-before-traffic" : "home-workspace"}>
        {!status.noHistory && <DashboardTraffic summary={summary} onViewHour={props.onViewHour} />}
        {(!status.noHistory || hasDiscoveries) &&
          <DashboardDiscoveries changes={summary.whats_new} onDevice={props.onDeviceSelect} onDomain={props.onDomainSelect}
            onList={props.onViewBlocklists} onRecord={props.onViewLocalDns} />}
        {(!status.noHistory || summary.observed_devices > 0) &&
          <button className="home-troubleshoot" onClick={props.onTroubleshoot}><LifeBuoy size={19} aria-hidden="true" /><span>Fix a broken site</span><ChevronRight size={17} aria-hidden="true" /></button>}
        {providers}
      </div>
      {!status.noHistory && <>
        <div className="dashboard-rank-grid">
          <RankPanel title="Top domains" items={summary.top_queried_domains} empty="No DNS activity yet today." showFavicons onSelect={props.onDomainSelect} />
          <RankPanel title="Blocked domains" items={summary.top_blocked_domains} empty="No domains blocked today." showFavicons tone="blocked" onSelect={props.onDomainSelect} />
          <RankPanel title="Top devices" items={summary.top_clients} empty="No device activity today." onViewAll={props.onViewDevices} onSelect={props.onDeviceSelect} />
        </div>
        <DashboardRecentActivity summary={summary} onDomainSelect={props.onDomainSelect} onDeviceSelect={props.onDeviceSelect} onViewActivity={props.onViewActivity} />
      </>}
    </div>
  );
}

function DashboardDiscoveries({ changes, onDevice, onDomain, onList, onRecord }: {
  changes: DashboardSummary["whats_new"];
  onDevice: (reference: string) => void;
  onDomain: (domain: string) => void;
  onList: (id?: number) => void;
  onRecord: (id?: number) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const groups = [
    { items: changes?.devices, label: "Device", icon: MonitorSmartphone, open: (item: WhatsNewItem) => onDevice(item.device_id ? `id:${item.device_id}` : item.client_ip || item.label) },
    { items: changes?.local_records, label: "Local name", icon: Database, open: (item: WhatsNewItem) => onRecord(item.id) },
    { items: changes?.blocklists, label: "Blocklist", icon: ListFilter, open: (item: WhatsNewItem) => onList(item.id) },
    { items: changes?.domains, label: "Domain", icon: Globe2, open: (item: WhatsNewItem) => onDomain(item.label) },
  ];
  const rows = groups.flatMap(({ items, ...group }) => (items ?? []).map((item) => ({ ...group, item })));
  return (
    <section className="panel home-discoveries" aria-labelledby="home-discoveries-title">
      <h2 id="home-discoveries-title">First seen today</h2>
      {rows.length === 0 && <p>No new devices, domains, blocklists or local names today.</p>}
      {(expanded ? rows : rows.slice(0, 3)).map(({ icon: Icon, item, label, open }) => (
        <button className="home-discovery-row" key={`${label}-${item.device_id ?? item.id ?? item.label}`} onClick={() => open(item)}>
          <Icon size={17} aria-hidden="true" />
          <span><strong>{item.label}</strong><small>{label}{item.client_ip && item.client_ip !== item.label && ` · ${item.client_ip}`}</small></span>
          <ChevronRight size={16} aria-hidden="true" />
        </button>
      ))}
      {rows.length > 3 && <Button variant="link" aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>{expanded ? "Show fewer" : `Show ${rows.length - 3} more`}</Button>}
    </section>
  );
}

function DashboardRecentActivity({ summary, onDomainSelect, onDeviceSelect, onViewActivity }: { readonly summary: DashboardSummary; readonly onDomainSelect: (domain: string) => void; readonly onDeviceSelect: (ip: string) => void; readonly onViewActivity: () => void }) {
  const recentActivity = useMemo(() => summary.recent_activity.slice(0, 8), [summary.recent_activity]);
  const activityColumns = useMemo<ColumnDef<typeof dashboardActivityFeatures, DNSQuery>[]>(() => [
    {
      accessorKey: "timestamp",
      header: "Time",
      cell: ({ row }) => <><strong>{formatTime(row.original.timestamp)}</strong><span>{formatDate(row.original.timestamp)}</span></>
    },
    {
      id: "result",
      header: "Result",
      cell: ({ row }) => row.original.action.toLowerCase() === "blocked" ? <Badge variant="destructive">Blocked</Badge> : <span className="dashboard-allowed-result">Allowed</span>
    },
    {
      id: "domain",
      header: "Domain",
      cell: ({ row }) => <button className="table-domain-link" type="button" onClick={() => onDomainSelect(row.original.domain)}><DomainFavicon domain={row.original.domain} /><span>{row.original.domain}</span></button>
    },
    {
      id: "device",
      header: "Device",
      cell: ({ row }) => <button className="device-link" type="button" title={row.original.client_ip} onClick={() => onDeviceSelect(row.original.client_ip)}>{row.original.device_name || row.original.client_ip}</button>
    },
    {
      id: "type",
      header: "Type",
      cell: ({ row }) => <span className="query-type-chip">{row.original.query_type}</span>
    },
    {
      id: "source",
      header: "Source",
      cell: ({ row }) => <ResolutionSource source={row.original.source} upstream={row.original.upstream} />
    }
  ], [onDomainSelect, onDeviceSelect]);
  const activityTable = useTable({
    features: dashboardActivityFeatures,
    data: recentActivity,
    columns: activityColumns,
    getRowId: (query, index) => `${query.id ?? "activity"}-${query.timestamp}-${query.domain}-${query.client_ip}-${index}`
  });

  return <section className="panel dashboard-activity-panel"><div className="panel-title dashboard-panel-title"><div><h2>Recent activity</h2></div><Button variant="link" className="text-action" type="button" onClick={onViewActivity}>View all activity</Button></div>{recentActivity.length === 0 ? <div className="compact-empty"><strong>No activity yet</strong><span>Point a device or router at Faro to start seeing DNS requests.</span></div> : <div className="dashboard-table-wrap"><Table className="monitor-table dashboard-activity-table"><thead>{activityTable.getHeaderGroups().map((headerGroup) => <tr key={headerGroup.id}>{headerGroup.headers.map((header) => <th key={header.id}>{header.isPlaceholder ? null : <activityTable.FlexRender header={header} />}</th>)}</tr>)}</thead><tbody>{activityTable.getRowModel().rows.map((row) => <tr key={row.id}>{row.getAllCells().map((cell) => <td key={cell.id} className={dashboardActivityCellClass(cell.column.id)}><activityTable.FlexRender cell={cell} /></td>)}</tr>)}</tbody></Table></div>}</section>;
}

function dashboardActivityCellClass(columnID: string) {
  return columnID === "timestamp" ? "time-cell stacked-time" : undefined;
}

function RankPanel({ title, items, empty, showFavicons = false, tone = "default", onSelect, onViewAll }: { readonly title: string; readonly items: CountItem[]; readonly empty: string; readonly showFavicons?: boolean; readonly tone?: "default" | "blocked"; readonly onSelect?: (label: string) => void; readonly onViewAll?: () => void }) {
  const max = Math.max(...items.map((item) => item.count), 1);
  return (
    <section className={`panel compact-rank-panel ${tone}`}>
      <div className="panel-title dashboard-panel-title">
        <h2>{title}</h2>
        {onViewAll && <Button variant="link" className="text-action" type="button" onClick={onViewAll}>View devices</Button>}
      </div>
      {items.length === 0 ? (
        <div className="compact-empty"><span>{empty}</span></div>
      ) : (
        <div className="compact-rank-list">
          {items.map((item, index) => (
            <div className="compact-rank-row" key={item.device_id ?? (item.client_ip || item.label)}>
              <span className="rank-position">{index + 1}</span>
              {showFavicons && <DomainFavicon domain={item.label} />}
              {onSelect ? (
                <Button variant="link" className="link-button justify-start text-foreground font-normal hover:text-accent-foreground" type="button" onClick={() => onSelect(item.device_id ? `id:${item.device_id}` : item.client_ip || item.label)} title={item.client_ip}>{item.label}</Button>
              ) : (
                <strong>{item.label}</strong>
              )}
              <span className="rank-count">{formatNumber(item.count)}</span>
              <span className="rank-meter"><i style={{ width: `${Math.max(5, (item.count / max) * 100)}%` }} /></span>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
