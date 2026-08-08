import { tableFeatures, useTable } from "@tanstack/react-table";
import type { ColumnDef } from "@tanstack/react-table";
import { useCallback, useMemo, useState, type SubmitEvent } from "react";
import { api, type DNSRecord, type Setting } from "../api/client";
import { EmptyState } from "../components/EmptyState";

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
  const localSuffix = settings.find((setting) => setting.key === "local_domain_suffix")?.value || "home";

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (editing) {
      await api.updateRecord({ ...editing, ...form });
      setEditing(null);
    } else {
      await api.createRecord(form);
    }
    setForm(blankRecord);
    await refresh();
  }

  const startEdit = useCallback((record: DNSRecord) => {
    setEditing(record);
    setForm({ hostname: record.hostname, type: record.type, value: record.value, description: record.description });
  }, []);

  const deleteRecord = useCallback(async (recordID: number) => {
    await api.deleteRecord(recordID);
    await refresh();
  }, [refresh]);

  const recordColumns = useMemo<ColumnDef<typeof localDnsFeatures, DNSRecord>[]>(() => [
    {
      accessorKey: "hostname",
      header: "Hostname",
      cell: ({ row }) => row.original.hostname
    },
    {
      accessorKey: "type",
      header: "Type",
      cell: ({ row }) => row.original.type
    },
    {
      accessorKey: "value",
      header: "Value",
      cell: ({ row }) => row.original.value
    },
    {
      accessorKey: "description",
      header: "Description",
      cell: ({ row }) => row.original.description
    },
    {
      id: "actions",
      header: () => <span className="sr-only">Actions</span>,
      enableSorting: false,
      cell: ({ row }) => <div className="row-actions"><button type="button" className="secondary" onClick={() => startEdit(row.original)}>Edit</button><button type="button" className="danger" onClick={() => void deleteRecord(row.original.id)}>Delete</button></div>
    }
  ], [deleteRecord, startEdit]);

  const recordTable = useTable({
    features: localDnsFeatures,
    data: records,
    columns: recordColumns,
    getRowId: (record) => String(record.id)
  });

  return (
    <div className="two-column">
      <section className="panel">
        <div className="panel-title">
          <h2>Local DNS records</h2>
        </div>
        {records.length === 0 ? (
          <EmptyState title="No local records yet" body="Add names for devices or services you want Faro to resolve on your network." />
        ) : (
        <div className="table-wrap">
          <table>
            <thead>
              {recordTable.getHeaderGroups().map((headerGroup) => (
                <tr key={headerGroup.id}>
                  {headerGroup.headers.map((header) => <th key={header.id}>{header.isPlaceholder ? null : <recordTable.FlexRender header={header} />}</th>)}
                </tr>
              ))}
            </thead>
            <tbody>
              {recordTable.getRowModel().rows.map((row) => <tr key={row.id}>{row.getAllCells().map((cell) => <td key={cell.id}><recordTable.FlexRender cell={cell} /></td>)}</tr>)}
            </tbody>
          </table>
        </div>
        )}
      </section>

      <section className="panel form-panel">
        <div className="panel-title">
          <h2>{editing ? "Edit record" : "Add local record"}</h2>
        </div>
        <form className="stack-form" onSubmit={(event) => void submit(event)}>
          <label>
            <span>Hostname</span>
            <input value={form.hostname} onChange={(event) => setForm({ ...form, hostname: event.target.value })} placeholder={`plex or plex.${localSuffix}`} />
            <small>Single-label names automatically use the .{localSuffix} suffix.</small>
          </label>
          <label>
            <span>Type</span>
            <select value={form.type} onChange={(event) => setForm({ ...form, type: event.target.value as DNSRecord["type"] })}>
              <option>A</option>
              <option>AAAA</option>
            </select>
          </label>
          <label>
            <span>Value</span>
            <input value={form.value} onChange={(event) => setForm({ ...form, value: event.target.value })} placeholder="IPv4 or IPv6 address" />
          </label>
          <label>
            <span>Description</span>
            <input value={form.description} onChange={(event) => setForm({ ...form, description: event.target.value })} placeholder="Media server" />
          </label>
          <button type="submit">{editing ? "Save record" : "Add record"}</button>
          {editing && (
            <button
              type="button"
              className="secondary"
              onClick={() => {
                setEditing(null);
                setForm(blankRecord);
              }}
            >
              Cancel
            </button>
          )}
        </form>
      </section>
    </div>
  );
}
