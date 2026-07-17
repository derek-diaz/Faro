import { useState } from "react";
import { api, type DNSRecord } from "../api/client";
import { EmptyState } from "../components/EmptyState";

type LocalDnsProps = {
  records: DNSRecord[];
  refresh: () => Promise<void>;
};

const blankRecord: Omit<DNSRecord, "id"> = {
  hostname: "",
  type: "A",
  value: "",
  description: ""
};

export function LocalDns({ records, refresh }: LocalDnsProps) {
  const [form, setForm] = useState<Omit<DNSRecord, "id">>(blankRecord);
  const [editing, setEditing] = useState<DNSRecord | null>(null);

  async function submit(event: React.FormEvent) {
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

  function startEdit(record: DNSRecord) {
    setEditing(record);
    setForm({ hostname: record.hostname, type: record.type, value: record.value, description: record.description });
  }

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
              <tr>
                <th>Hostname</th>
                <th>Type</th>
                <th>Value</th>
                <th>Description</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {records.map((record) => (
                <tr key={record.id}>
                  <td>{record.hostname}</td>
                  <td>{record.type}</td>
                  <td>{record.value}</td>
                  <td>{record.description}</td>
                  <td className="row-actions">
                    <button type="button" className="secondary" onClick={() => startEdit(record)}>
                      Edit
                    </button>
                    <button
                      type="button"
                      className="danger"
                      onClick={async () => {
                        await api.deleteRecord(record.id);
                        await refresh();
                      }}
                    >
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
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
            Hostname
            <input value={form.hostname} onChange={(event) => setForm({ ...form, hostname: event.target.value })} placeholder="plex.home" />
          </label>
          <label>
            Type
            <select value={form.type} onChange={(event) => setForm({ ...form, type: event.target.value as DNSRecord["type"] })}>
              <option>A</option>
              <option>AAAA</option>
            </select>
          </label>
          <label>
            Value
            <input value={form.value} onChange={(event) => setForm({ ...form, value: event.target.value })} placeholder="192.168.7.50" />
          </label>
          <label>
            Description
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
