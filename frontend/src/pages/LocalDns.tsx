import { Table } from "../components/ui/table";
import { NativeSelect } from "../components/ui/native-select";
import { Input } from "../components/ui/input";
import { Button } from "../components/ui/button";
import { tableFeatures, useTable } from "@tanstack/react-table";
import type { ColumnDef } from "@tanstack/react-table";
import { useCallback, useMemo, useRef, useState, type SubmitEvent } from "react";
import { api, type DNSRecord, type Setting } from "../api/client";
import { AlertTriangle, Check, Pencil, Plus, RefreshCw, Router, Trash2 } from "lucide-react";

type LocalDnsProps = {
  readonly records: DNSRecord[];
  readonly settings: Setting[];
  readonly refresh: () => Promise<void>;
};

const blankRecord: Omit<DNSRecord, "id"> = {
  hostname: "",
  type: "A",
  value: "",
  description: ""
};

const localDnsFeatures = tableFeatures({});

export function LocalDns({ records, settings, refresh }: LocalDnsProps) {
  const [form, setForm] = useState<Omit<DNSRecord, "id">>(blankRecord);
  const [editing, setEditing] = useState<DNSRecord | null>(null);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState<number | null>(null);
  const [formError, setFormError] = useState("");
  const [listError, setListError] = useState("");
  const [notice, setNotice] = useState("");
  const hostnameInput = useRef<HTMLInputElement>(null);
  const busy = saving || deleting !== null;
  const localSuffix = settings.find((setting) => setting.key === "local_domain_suffix")?.value || "home";

  const refreshRecords = useCallback(async () => {
    try { await refresh(); setListError(""); }
    catch { setListError("Couldn’t refresh the record list. Your saved changes are still applied."); }
  }, [refresh]);

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    setSaving(true);
    setFormError("");
    setNotice("");
    try {
      if (editing) await api.updateRecord({ ...editing, ...form });
      else await api.createRecord(form);
      setNotice(editing ? "Record updated." : "Record added.");
      setEditing(null);
      setForm(blankRecord);
      await refreshRecords();
    } catch (error) {
      setFormError(error instanceof Error ? error.message : "Couldn’t save this record. Try again.");
    } finally { setSaving(false); }
  }

  const startEdit = useCallback((record: DNSRecord) => {
    setEditing(record);
    setFormError("");
    setNotice("");
    setForm({ hostname: record.hostname, type: record.type, value: record.value, description: record.description });
    hostnameInput.current?.focus();
  }, []);

  const deleteRecord = useCallback(async (record: DNSRecord) => {
    setDeleting(record.id);
    setListError("");
    setNotice("");
    try {
      await api.deleteRecord(record.id);
      if (editing?.id === record.id) { setEditing(null); setForm(blankRecord); setFormError(""); }
      setNotice(`${record.hostname} removed.`);
      await refreshRecords();
    } catch (error) {
      setListError(error instanceof Error ? error.message : "Couldn’t delete this record. Try again.");
    } finally { setDeleting(null); }
  }, [editing?.id, refreshRecords]);

  const recordColumns = useMemo<ColumnDef<typeof localDnsFeatures, DNSRecord>[]>(() => [
    {
      accessorKey: "hostname",
      header: "Hostname",
      cell: ({ row }) => <div className="local-record-name"><strong>{row.original.hostname}</strong>{row.original.description && <small>{row.original.description}</small>}</div>
    },
    {
      accessorKey: "type",
      header: "Type",
      cell: ({ row }) => <span className="local-record-type">{row.original.type}</span>
    },
    {
      accessorKey: "value",
      header: "Address",
      cell: ({ row }) => <code className="local-record-address">{row.original.value}</code>
    },
    {
      id: "actions",
      header: () => <span className="sr-only">Actions</span>,
      enableSorting: false,
      cell: ({ row }) => <div className="local-record-actions"><Button variant="ghost" size="icon-sm" type="button" disabled={busy} aria-label={`Edit ${row.original.hostname}`} title={`Edit ${row.original.hostname}`} onClick={() => startEdit(row.original)}><Pencil size={15} /></Button><Button variant="ghost" size="icon-sm" type="button" className="text-muted-foreground hover:text-destructive" disabled={busy} aria-label={`Delete ${row.original.hostname}`} title={`Delete ${row.original.hostname}`} onClick={() => void deleteRecord(row.original)}>{deleting === row.original.id ? <RefreshCw size={15} className="animate-spin" /> : <Trash2 size={15} />}</Button></div>
    }
  ], [busy, deleting, deleteRecord, startEdit]);

  const recordTable = useTable({
    features: localDnsFeatures,
    data: records,
    columns: recordColumns,
    getRowId: (record) => String(record.id)
  });

  return (
    <div className="local-dns-page">
      <section className="panel local-records-panel" aria-label="Local DNS records">
        {records.length > 0 && <div className="local-records-toolbar"><span>{records.length} {records.length === 1 ? "record" : "records"}</span></div>}
        {listError && <div className="local-dns-error" role="alert"><AlertTriangle size={17} /><p>{listError}</p><Button variant="outline" size="sm" disabled={busy} onClick={() => void refreshRecords()}>Refresh</Button></div>}
        {notice && <div className="local-dns-notice" role="status"><Check size={15} />{notice}</div>}
        {records.length === 0 ? (
          <div className="local-records-empty"><span className="local-dns-empty-icon"><Router size={25} /></span><h2>No local records yet</h2><p>Give a device or service a name you can remember.</p><span className="local-dns-example"><code>plex.{localSuffix}</code><span aria-hidden="true">→</span><code>192.168.1.20</code></span><Button variant="outline" size="sm" onClick={() => hostnameInput.current?.focus()}><Plus size={15} />Create your first record</Button></div>
        ) : (
        <div className="table-wrap local-records-table">
          <Table aria-label="Local DNS records">
            <thead>
              {recordTable.getHeaderGroups().map((headerGroup) => (
                <tr key={headerGroup.id}>
                  {headerGroup.headers.map((header) => <th key={header.id}>{header.isPlaceholder ? null : <recordTable.FlexRender header={header} />}</th>)}
                </tr>
              ))}
            </thead>
            <tbody>
              {recordTable.getRowModel().rows.map((row) => <tr key={row.id} data-editing={editing?.id === row.original.id || undefined}>{row.getAllCells().map((cell) => <td key={cell.id}><recordTable.FlexRender cell={cell} /></td>)}</tr>)}
            </tbody>
          </Table>
        </div>
        )}
      </section>

      <section className="panel form-panel local-record-form" aria-labelledby="local-record-form-title">
        <div className="panel-title">
          <h2 id="local-record-form-title">{editing ? "Edit record" : "New record"}</h2>
        </div>
        <form className="stack-form" onSubmit={(event) => void submit(event)} aria-busy={saving}>
          <fieldset disabled={busy}>
            <label>
              <span>Hostname</span>
              <Input ref={hostnameInput} aria-label="Hostname" aria-describedby="local-hostname-help" value={form.hostname} onChange={(event) => setForm({ ...form, hostname: event.target.value })} placeholder={`plex or plex.${localSuffix}`} />
              <small id="local-hostname-help">A name like <code>plex</code> becomes <code>plex.{localSuffix}</code>.</small>
            </label>
            <label>
              <span>Type</span>
              <NativeSelect value={form.type} onChange={(event) => setForm({ ...form, type: event.target.value as DNSRecord["type"] })}>
                <option value="A">A · IPv4 address</option>
                <option value="AAAA">AAAA · IPv6 address</option>
              </NativeSelect>
            </label>
            <label>
              <span>{form.type === "AAAA" ? "IPv6 address" : "IPv4 address"}</span>
              <Input value={form.value} onChange={(event) => setForm({ ...form, value: event.target.value })} placeholder={form.type === "AAAA" ? "fd00::20" : "192.168.1.20"} />
            </label>
            <label>
              <span>Description <em>Optional</em></span>
              <Input value={form.description} onChange={(event) => setForm({ ...form, description: event.target.value })} placeholder="Media server" />
            </label>
            {formError && <div className="local-dns-form-error" role="alert"><AlertTriangle size={16} /><span>{formError}</span></div>}
            <div className="local-record-form-actions"><Button variant="default" type="submit">{saving ? <RefreshCw size={15} className="animate-spin" /> : editing ? <Check size={15} /> : <Plus size={15} />}{saving ? "Saving…" : editing ? "Save record" : "Add record"}</Button>
            {editing && (
              <Button variant="outline"
                type="button"
                className="secondary"
                onClick={() => {
                  setEditing(null);
                  setForm(blankRecord);
                  setFormError("");
                }}
              >
                Cancel
              </Button>
            )}
            </div>
          </fieldset>
        </form>
      </section>
    </div>
  );
}
