import {
  Baby,
  BriefcaseBusiness,
  Check,
  CalendarClock,
  ChevronLeft,
  ChevronRight,
  Cpu,
  Gamepad2,
  Globe2,
  House,
  Laptop,
  Lightbulb,
  ListFilter,
  LoaderCircle,
  MonitorPlay,
  Pause,
  Play,
  Plus,
  Save,
  Settings2,
  Shield,
  ShieldCheck,
  Smartphone,
  Trash2,
  Tv,
  UserRound,
  Users,
  X
} from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode, type SubmitEvent } from "react";
import {
  api,
  type Blocklist,
  type DeviceSummary,
  type Protection,
  type ProtectionIconKey,
  type ProtectionInput
} from "../api/client";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { errorMessage } from "../utils/formatting";

type Props = {
  readonly protections: Protection[];
  readonly blocklists: Blocklist[];
  readonly devices: DeviceSummary[];
  readonly refresh: () => Promise<void>;
  readonly onManageBlocklists: () => void;
};

type Draft = ProtectionInput;

const icons: { key: ProtectionIconKey; label: string; icon: typeof House }[] = [
  { key: "house", label: "Home", icon: House },
  { key: "users", label: "Family", icon: Users },
  { key: "baby", label: "Children", icon: Baby },
  { key: "guest", label: "Guests", icon: UserRound },
  { key: "tv", label: "TV", icon: Tv },
  { key: "gamepad", label: "Gaming", icon: Gamepad2 },
  { key: "smartphone", label: "Phone", icon: Smartphone },
  { key: "laptop", label: "Computer", icon: Laptop },
  { key: "briefcase", label: "Work", icon: BriefcaseBusiness },
  { key: "lightbulb", label: "Smart home", icon: Lightbulb },
  { key: "cpu", label: "Server", icon: Cpu },
  { key: "shield", label: "Protection", icon: Shield }
];

const emptyDraft = (): Draft => ({
  name: "",
  icon: "shield",
  blocklist_ids: [],
  allow_domains: [],
  block_domains: [],
  device_ips: [],
  schedule: { enabled: false, days: "1,2,3,4,5,6,7", start: "08:00", end: "22:00", timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC" }
});

export function ProtectionPage({ protections, blocklists, devices, refresh, onManageBlocklists }: Props) {
  const [selectedID, setSelectedID] = useState<number | null>(null);
  const [editor, setEditor] = useState<Draft>(emptyDraft);
  const [wizard, setWizard] = useState(false);
  const [step, setStep] = useState(0);
  const [draft, setDraft] = useState<Draft>(emptyDraft);
  const [busy, setBusy] = useState(false);
  const [saveStage, setSaveStage] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [pauseBusyID, setPauseBusyID] = useState<number | null>(null);

  const selected = protections.find((item) => item.id === selectedID) ?? null;

  useEffect(() => {
    if (selected) setEditor(protectionDraft(selected));
  }, [selected]);

  const assignedIPs = useMemo(() => new Set(protections.flatMap((item) => item.is_default ? [] : item.device_ips)), [protections]);
  const homeDevices = devices.filter((device) => !assignedIPs.has(device.client_ip));

  function openWizard() {
    setDraft(emptyDraft());
    setStep(0);
    setError("");
    setNotice("");
    setWizard(true);
  }

  async function saveSelected(event: SubmitEvent) {
    event.preventDefault();
    if (!selected) return;
    setBusy(true);
    setSaveStage("Saving settings and applying DNS changes");
    setError("");
    setNotice("");
    try {
      await api.updateProtection(selected.id, cleanInput(editor));
      setSaveStage("Refreshing the updated protection details");
      await refresh();
      setNotice(`${editor.name} was updated.`);
    } catch (error_) {
      setError(errorMessage(error_, "Could not save protection."));
    } finally {
      setSaveStage("");
      setBusy(false);
    }
  }

  async function finishWizard() {
    setBusy(true);
    setError("");
    try {
      await api.createProtection(cleanInput(draft));
      await refresh();
      setSelectedID(null);
      setWizard(false);
      setNotice(`${draft.name} is ready.`);
    } catch (error_) {
      setError(errorMessage(error_, "Could not create protection."));
    } finally {
      setBusy(false);
    }
  }

  async function removeSelected() {
    if (!selected || selected.is_default) return;
    setBusy(true);
    setError("");
    try {
      await api.deleteProtection(selected.id);
      setSelectedID(null);
      await refresh();
      setNotice(`${selected.name} was deleted. Its devices now use Home.`);
    } catch (error_) {
      setError(errorMessage(error_, "Could not delete protection."));
    } finally {
      setBusy(false);
      setConfirmDelete(false);
    }
  }

  async function changePause(protection: Protection, preset: "5m" | "1h" | "tomorrow" | "resume") {
    setPauseBusyID(protection.id);
    setError("");
    setNotice("");
    try {
      await api.pauseProtection(protection.id, pauseUntil(preset));
      await refresh();
      setNotice(preset === "resume" ? `${protection.name} is blocking domains again.` : `${protection.name} will temporarily allow domains it normally blocks.`);
    } catch (error_) {
      setError(errorMessage(error_, "Could not change the protection pause."));
    } finally {
      setPauseBusyID(null);
    }
  }

  if (!protections.length) return <div className="panel protection-empty">Preparing Home protection…</div>;

  return (
    <div className="protection-page">
      {(error || notice) && <output className={`protection-message ${error ? "error" : "success"}`}><span>{error || notice}</span><button className="icon-button" type="button" aria-label="Dismiss" onClick={() => { setError(""); setNotice(""); }}><X size={15} /></button></output>}

      {!selected ? <ProtectionOverview protections={protections} blocklists={blocklists} devices={devices} homeDevices={homeDevices} pauseBusyID={pauseBusyID} onPause={changePause} onManage={(id) => { setSelectedID(id); setError(""); setNotice(""); }} onCreate={openWizard} /> : <ProtectionEditor selected={selected} editor={editor} setEditor={setEditor} blocklists={blocklists} devices={devices} homeDevices={homeDevices} saveStage={saveStage} busy={busy} onBack={() => { setSelectedID(null); setError(""); }} onDelete={() => setConfirmDelete(true)} onManageBlocklists={onManageBlocklists} saveSelected={saveSelected} />}

      {wizard && <ProtectionWizard step={step} setStep={setStep} draft={draft} setDraft={setDraft} blocklists={blocklists} devices={devices} busy={busy} error={error} onManageBlocklists={() => { setWizard(false); onManageBlocklists(); }} onClose={() => { if (!busy) { setWizard(false); setError(""); } }} onFinish={() => void finishWizard()} />}
      {confirmDelete && selected && !selected.is_default && <ConfirmDialog title={`Delete ${selected.name}?`} body="This protection setup and its exceptions will be permanently deleted." confirmLabel="Delete protection" busyLabel="Deleting protection…" busy={busy} onCancel={() => setConfirmDelete(false)} onConfirm={() => void removeSelected()} detail={<div className="confirm-dialog-impact warning"><Shield size={18} /><span><strong>{selected.device_ips.length} assigned device{selected.device_ips.length === 1 ? "" : "s"} will return to Home</strong><small>The devices themselves and their activity history will remain.</small></span></div>} />}
    </div>
  );
}

function ProtectionEditor({ selected, editor, setEditor, blocklists, devices, homeDevices, saveStage, busy, onBack, onDelete, onManageBlocklists, saveSelected }: {
  readonly selected: Protection;
  readonly editor: Draft;
  readonly setEditor: (draft: Draft) => void;
  readonly blocklists: Blocklist[];
  readonly devices: DeviceSummary[];
  readonly homeDevices: DeviceSummary[];
  readonly saveStage: string;
  readonly busy: boolean;
  readonly onBack: () => void;
  readonly onDelete: () => void;
  readonly onManageBlocklists: () => void;
  readonly saveSelected: (event: SubmitEvent) => Promise<void>;
}) {
  const enabledBlocklists = blocklists.filter((item) => item.enabled);
  const defaultCopy = selected.is_default ? "Any device not assigned elsewhere uses Home." : "Only the devices selected below use these choices.";
  return <div className="protection-edit-view">
    <div className="protection-edit-nav"><button className="secondary" type="button" disabled={Boolean(saveStage)} onClick={onBack}><ChevronLeft size={16} />All protection setups</button><span><small>Editing setup</small><strong>{selected.name}</strong></span></div>
    <form className={`protection-editor panel ${saveStage ? "saving" : ""}`} aria-busy={Boolean(saveStage)} onSubmit={(event) => void saveSelected(event)}>
      <div className="protection-editor-heading"><div className="protection-title-icon"><ProtectionIcon name={editor.icon} /></div><div><span>{selected.is_default ? "NETWORK DEFAULT" : "CUSTOM PROTECTION"}</span><input value={editor.name} disabled={selected.is_default} maxLength={40} aria-label="Protection name" onChange={(event) => setEditor({ ...editor, name: event.target.value })} /><p>{defaultCopy}</p></div>{!selected.is_default && <button className="icon-button danger-icon" type="button" title="Delete protection" aria-label={`Delete ${selected.name}`} disabled={Boolean(saveStage)} onClick={onDelete}><Trash2 size={17} /></button>}</div>
      <EditorSection title="Icon" description="Choose a simple visual cue."><IconPicker value={editor.icon} onChange={(icon) => setEditor({ ...editor, icon })} compact /></EditorSection>
      <EditorSection title="Blocking" description="Select installed lists for this protection setup."><div className="protection-choice-grid">{enabledBlocklists.map((blocklist) => <label key={blocklist.id} className={editor.blocklist_ids.includes(blocklist.id) ? "selected" : ""}><input type="checkbox" checked={editor.blocklist_ids.includes(blocklist.id)} onChange={() => setEditor({ ...editor, blocklist_ids: toggleNumber(editor.blocklist_ids, blocklist.id) })} /><span><strong>{blocklist.name}</strong><small>{blocklist.entry_count.toLocaleString()} domains</small></span><Check size={15} /></label>)}{enabledBlocklists.length === 0 && <p className="protection-inline-empty">No active blocklists are installed. Add one from the Blocklists page first.</p>}</div><button className="secondary protection-add-list" type="button" onClick={onManageBlocklists}>Manage blocklists</button></EditorSection>
      <EditorSection title="Exceptions" description="Use one exact domain per line. Allowed domains always win."><div className="protection-domain-editors"><label><span>Always allow</span><textarea value={editor.allow_domains.join("\n")} onChange={(event) => setEditor({ ...editor, allow_domains: lines(event.target.value) })} placeholder="example.com" /></label><label><span>Always block</span><textarea value={editor.block_domains.join("\n")} onChange={(event) => setEditor({ ...editor, block_domains: lines(event.target.value) })} placeholder="telemetry.example" /></label></div></EditorSection>
      <EditorSection title="Domain blocking schedule" description="Choose when Faro applies this setup’s blocklists and custom blocks. This schedule never disconnects devices."><ScheduleEditor draft={editor} setDraft={setEditor} /></EditorSection>
      <EditorSection title="Devices" description={selected.is_default ? "Home automatically covers every device not assigned elsewhere." : "Choose which observed devices use this protection."}>{selected.is_default ? <DeviceSummaryList devices={homeDevices} /> : <DeviceChoices devices={devices} selected={editor.device_ips} onChange={(deviceIPs) => setEditor({ ...editor, device_ips: deviceIPs })} />}</EditorSection>
      <footer className={`protection-save-bar ${saveStage ? "saving" : ""}`}><ProtectionSaveProgress saveStage={saveStage} selectedName={selected.name} busy={busy} editorName={editor.name} /></footer>
    </form>
  </div>;
}

function ProtectionSaveProgress({ saveStage, selectedName, busy, editorName }: { readonly saveStage: string; readonly selectedName: string; readonly busy: boolean; readonly editorName: string }) {
  return <>{saveStage ? <output className="protection-save-progress" aria-live="polite"><span className="protection-save-spinner"><LoaderCircle className="spinning" size={17} /></span><div><strong>Saving {selectedName}</strong><small>{saveStage}. Keep this page open.</small><i aria-hidden="true"><b /></i></div></output> : <span>Changes apply to DNS as soon as they are saved.</span>}<button type="submit" disabled={busy || !editorName.trim()}>{saveStage ? <LoaderCircle className="spinning" size={16} /> : <Save size={16} />}<span>{saveStage ? "Saving changes…" : "Save changes"}</span></button></>;
}

function ProtectionOverview({ protections, blocklists, devices, homeDevices, pauseBusyID, onPause, onManage, onCreate }: {
  readonly protections: Protection[];
  readonly blocklists: Blocklist[];
  readonly devices: DeviceSummary[];
  readonly homeDevices: DeviceSummary[];
  readonly pauseBusyID: number | null;
  readonly onPause: (protection: Protection, preset: "5m" | "1h" | "tomorrow" | "resume") => Promise<void>;
  readonly onManage: (id: number) => void;
  readonly onCreate: () => void;
}) {
  return (
    <section className="protection-overview" aria-label="Protection setups">
      <div className="protection-overview-toolbar">
        <div className="protection-overview-actions">
          <span className="protection-overview-count"><strong>{protections.filter((item) => item.is_active).length}</strong> setup{protections.filter((item) => item.is_active).length === 1 ? "" : "s"} blocking domains now</span>
          <button type="button" onClick={onCreate}><Plus size={16} />New protection setup</button>
        </div>
      </div>

      <div className="protection-card-grid">
        {protections.map((protection) => {
          const installed = blocklists.filter((item) => protection.blocklist_ids.includes(item.id));
          const protectedDevices = protection.is_default ? homeDevices : devices.filter((device) => protection.device_ips.includes(device.client_ip));
          const exceptionCount = protection.allow_entries.length + protection.block_entries.length;
          return (
            <article className={`protection-summary-card panel ${protection.is_default ? "default" : ""} ${protection.is_active ? "" : "inactive"}`} key={protection.id}>
              <div className="protection-card-top">
                <span className="protection-card-icon"><ProtectionIcon name={protection.icon} /></span>
                <span className="protection-card-copy"><small>{protectionStateLabel(protection)}</small><strong>{protection.name}</strong><p>{protection.is_default ? "Automatically protects every device that does not use another setup." : "Applies only to the devices assigned below."}</p></span>
                <button className="secondary protection-card-manage" type="button" onClick={() => onManage(protection.id)}><Settings2 size={15} />Edit</button>
              </div>
              <div className="protection-card-facts">
                <div><ListFilter size={16} /><span><small>Blocking</small><strong>{installed.length ? installed.map((item) => item.name).join(", ") : "No lists selected"}</strong></span></div>
                <div><MonitorPlay size={16} /><span><small>Devices</small><strong>{protectedDeviceLabel(protectedDevices.length, protection.is_default)}</strong></span></div>
                <div><Shield size={16} /><span><small>Exceptions</small><strong>{customRuleLabel(exceptionCount)}</strong></span></div>
                <div><CalendarClock size={16} /><span><small>Schedule</small><strong>{scheduleLabel(protection)}</strong></span></div>
              </div>
              <div className={`protection-temporary-control ${protection.state === "paused" ? "paused" : ""}`}>
                <span className="protection-temporary-icon">{protection.state === "paused" ? <Pause size={17} /> : <ShieldCheck size={17} />}</span>
                <div className="protection-temporary-copy"><small>{protection.state === "paused" ? "DOMAIN BLOCKING IS OFF" : "TEMPORARY EXCEPTION"}</small><strong>{protection.state === "paused" ? `Allowing all domains until ${formatPauseTime(protection.paused_until)}` : "Temporarily stop domain blocking"}</strong><p>Devices stay online. Faro {protection.state === "paused" ? "is allowing" : "will allow"} domains this setup would normally block.</p></div>
                <div className="protection-pause-actions" aria-label={`Temporarily stop domain blocking for ${protection.name}`}>
                  {protection.state === "paused" ? <button type="button" disabled={pauseBusyID === protection.id} onClick={() => void onPause(protection, "resume")}><Play size={14} />Start blocking again</button> : <><button className="secondary" type="button" disabled={pauseBusyID === protection.id} onClick={() => void onPause(protection, "5m")}>For 5 min</button><button className="secondary" type="button" disabled={pauseBusyID === protection.id} onClick={() => void onPause(protection, "1h")}>For 1 hour</button><button className="secondary" type="button" disabled={pauseBusyID === protection.id} onClick={() => void onPause(protection, "tomorrow")}>Until tomorrow</button></>}
                </div>
              </div>
            </article>
          );
        })}
      </div>
    </section>
  );
}

function ProtectionWizard({ step, setStep, draft, setDraft, blocklists, devices, busy, error, onManageBlocklists, onClose, onFinish }: {
  readonly step: number;
  readonly setStep: (step: number) => void;
  readonly draft: Draft;
  readonly setDraft: (draft: Draft) => void;
  readonly blocklists: Blocklist[];
  readonly devices: DeviceSummary[];
  readonly busy: boolean;
  readonly error: string;
  readonly onManageBlocklists: () => void;
  readonly onClose: () => void;
  readonly onFinish: () => void;
}) {
  const titles = ["Name your protection", "Choose what to block", "Add exceptions", "Choose devices", "Review and create"];
  const canContinue = step !== 0 || draft.name.trim().length > 0;
  return (
    <dialog open className="protection-wizard-backdrop" aria-labelledby="protection-wizard-title">
      <section className="protection-wizard">
        <header><div><span>STEP {step + 1} OF 5</span><h2 id="protection-wizard-title">{titles[step]}</h2></div><button className="icon-button" type="button" aria-label="Close" disabled={busy} onClick={onClose}><X size={19} /></button></header>
        <div className="protection-step-track" aria-hidden="true">{titles.map((_, index) => <i key={index} className={index <= step ? "active" : ""} />)}</div>
        <div className="protection-wizard-body">
          {step === 0 && <><p>Pick a name people in your home will recognize, then choose an icon.</p><label className="protection-name-field"><span>Name</span><input autoFocus value={draft.name} maxLength={40} onChange={(event) => setDraft({ ...draft, name: event.target.value })} placeholder="Children, Guests, Work…" /></label><IconPicker value={draft.icon} onChange={(icon) => setDraft({ ...draft, icon })} /></>}
          {step === 1 && <><p>Choose from the blocklists already installed in Faro. You can add or update sources from the separate Blocklists page.</p>{blocklists.some((item) => item.enabled) ? <div className="wizard-blocklist-grid">{blocklists.filter((item) => item.enabled).map((item) => { const selected = draft.blocklist_ids.includes(item.id); return <button type="button" className={selected ? "selected" : ""} key={item.id} onClick={() => setDraft({ ...draft, blocklist_ids: toggleNumber(draft.blocklist_ids, item.id) })}><span>{selected && <Check size={14} />}Installed</span><strong>{item.name}</strong><small>{item.entry_count.toLocaleString()} domains</small><em>{sourceHost(item.url)}</em></button>; })}</div> : <div className="wizard-no-blocklists"><strong>No active blocklists</strong><span>Install a source first, then return to create this protection setup.</span><button type="button" className="secondary" onClick={onManageBlocklists}>Open Blocklists</button></div>}</>}
          {step === 2 && <><p>Optional. Add exact domains that should behave differently for this protection.</p><div className="protection-domain-editors"><label><span>Always allow</span><textarea value={draft.allow_domains.join("\n")} onChange={(event) => setDraft({ ...draft, allow_domains: lines(event.target.value) })} placeholder="school.example" /></label><label><span>Always block</span><textarea value={draft.block_domains.join("\n")} onChange={(event) => setDraft({ ...draft, block_domains: lines(event.target.value) })} placeholder="social.example" /></label></div></>}
          {step === 3 && <><p>Select observed devices now. You can always assign more from the Devices page.</p><DeviceChoices devices={devices} selected={draft.device_ips} onChange={(deviceIPs) => setDraft({ ...draft, device_ips: deviceIPs })} /></>}
          {step === 4 && <Review draft={draft} blocklists={blocklists} devices={devices} />}
          {error && <div className="protection-wizard-error">{error}</div>}
        </div>
        <footer><button className="secondary" type="button" disabled={busy} onClick={() => step === 0 ? onClose() : setStep(step - 1)}><ChevronLeft size={16} />{step === 0 ? "Cancel" : "Back"}</button>{step < 4 ? <button type="button" disabled={!canContinue} onClick={() => setStep(step + 1)}>Continue<ChevronRight size={16} /></button> : <button type="button" disabled={busy} onClick={onFinish}><Shield size={16} />{busy ? "Creating…" : "Create protection"}</button>}</footer>
      </section>
    </dialog>
  );
}

function Review({ draft, blocklists, devices }: { readonly draft: Draft; readonly blocklists: Blocklist[]; readonly devices: DeviceSummary[] }) {
  const installedNames = blocklists.filter((item) => draft.blocklist_ids.includes(item.id)).map((item) => item.name);
  return <div className="protection-review"><div className="protection-review-identity"><ProtectionIcon name={draft.icon} /><span><strong>{draft.name}</strong><small>Ready to create</small></span></div><ReviewRow label="Blocking" value={installedNames.join(", ") || "No blocklists"} /><ReviewRow label="Always allow" value={`${draft.allow_domains.length} domain${draft.allow_domains.length === 1 ? "" : "s"}`} /><ReviewRow label="Always block" value={`${draft.block_domains.length} domain${draft.block_domains.length === 1 ? "" : "s"}`} /><ReviewRow label="Devices" value={draft.device_ips.length ? devices.filter((device) => draft.device_ips.includes(device.client_ip)).map(deviceName).join(", ") : "Assign later"} /></div>;
}

function ReviewRow({ label, value }: { readonly label: string; readonly value: string }) { return <div className="protection-review-row"><span>{label}</span><strong>{value}</strong></div>; }
function EditorSection({ title, description, children }: { readonly title: string; readonly description: string; readonly children: ReactNode }) { return <section className="protection-editor-section"><header><h3>{title}</h3><p>{description}</p></header><div>{children}</div></section>; }

function ScheduleEditor({ draft, setDraft }: { readonly draft: Draft; readonly setDraft: (draft: Draft) => void }) {
  const dayOptions = [[1, "M"], [2, "T"], [3, "W"], [4, "T"], [5, "F"], [6, "S"], [7, "S"]] as const;
  const selectedDays = draft.schedule.days.split(",").filter(Boolean).map(Number);
  const updateSchedule = (change: Partial<Draft["schedule"]>) => setDraft({ ...draft, schedule: { ...draft.schedule, ...change } });
  return <div className="protection-schedule-editor">
    <div className="protection-schedule-modes" role="radiogroup" aria-label="When to block domains">
      <button type="button" role="radio" aria-checked={!draft.schedule.enabled} className={!draft.schedule.enabled ? "selected" : ""} onClick={() => updateSchedule({ enabled: false })}><ShieldCheck size={18} /><span><strong>Block domains all the time</strong><small>Blocklists and custom rules apply every day, all day.</small></span><Check size={15} /></button>
      <button type="button" role="radio" aria-checked={draft.schedule.enabled} className={draft.schedule.enabled ? "selected" : ""} onClick={() => updateSchedule({ enabled: true, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC", ...(draft.schedule.start === "00:00" && draft.schedule.end === "23:59" ? { start: "08:00", end: "22:00" } : {}) })}><CalendarClock size={18} /><span><strong>Block only during selected hours</strong><small>Outside those hours, devices stay online and blocked domains are allowed.</small></span><Check size={15} /></button>
    </div>
    {draft.schedule.enabled && <div className="protection-schedule-fields">
      <div><span className="protection-schedule-field-label">Blocking days</span><div className="protection-schedule-days" aria-label="Days when domain blocking is active">{dayOptions.map(([day, label]) => <button type="button" className={selectedDays.includes(day) ? "selected" : ""} aria-pressed={selectedDays.includes(day)} aria-label={["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"][day - 1]} key={day} onClick={() => updateSchedule({ days: toggleDay(draft.schedule.days, day) })}>{label}</button>)}</div></div>
      <div className="protection-schedule-times"><label><span>Blocking starts</span><input type="time" value={draft.schedule.start} onChange={(event) => updateSchedule({ start: event.target.value })} /></label><span>to</span><label><span>Blocking ends</span><input type="time" value={draft.schedule.end} onChange={(event) => updateSchedule({ end: event.target.value })} /></label></div>
      <small className="protection-schedule-timezone">Times use {draft.schedule.timezone.replace(/_/g, " ")}.</small>
      <div className="protection-schedule-outcomes">
        <div><ShieldCheck size={18} /><span><small>During these hours</small><strong>Domain blocking is on</strong><p>Faro applies this setup’s blocklists, custom blocks, and allow exceptions.</p></span></div>
        <div><Globe2 size={18} /><span><small>Outside these hours</small><strong>Devices stay online</strong><p>Public domains are allowed. Faro Local DNS names still work.</p></span></div>
      </div>
    </div>}
    {!draft.schedule.enabled && <div className="protection-schedule-always"><ShieldCheck size={18} /><span><strong>Domain blocking stays on 24/7</strong><small>This affects filtering only; the devices themselves remain online.</small></span></div>}
  </div>;
}

function IconPicker({ value, onChange, compact = false }: { readonly value: ProtectionIconKey; readonly onChange: (value: ProtectionIconKey) => void; readonly compact?: boolean }) {
  return <div className={`protection-icon-picker ${compact ? "compact" : ""}`}>{icons.map((item) => { const Icon = item.icon; return <button type="button" className={value === item.key ? "selected" : ""} title={item.label} aria-label={item.label} aria-pressed={value === item.key} key={item.key} onClick={() => onChange(item.key)}><Icon size={compact ? 18 : 22} />{!compact && <span>{item.label}</span>}</button>; })}</div>;
}

export function ProtectionIcon({ name, size = 20 }: { readonly name: ProtectionIconKey; readonly size?: number }) { const Icon = icons.find((item) => item.key === name)?.icon ?? Shield; return <Icon size={size} />; }

function DeviceChoices({ devices, selected, onChange }: { readonly devices: DeviceSummary[]; readonly selected: string[]; readonly onChange: (selected: string[]) => void }) {
  if (!devices.length) return <p className="protection-inline-empty">No devices have been observed yet. You can assign devices later.</p>;
  return <div className="protection-device-choices">{devices.map((device) => <label className={selected.includes(device.client_ip) ? "selected" : ""} key={device.client_ip}><input type="checkbox" checked={selected.includes(device.client_ip)} onChange={() => onChange(toggleString(selected, device.client_ip))} /><MonitorPlay size={17} /><span><strong>{deviceName(device)}</strong><small>{device.client_ip}{device.protection ? ` · currently ${device.protection}` : ""}</small></span><Check size={15} /></label>)}</div>;
}

function DeviceSummaryList({ devices }: { readonly devices: DeviceSummary[] }) { return devices.length ? <div className="protection-device-summary">{devices.map((device) => <span key={device.client_ip}><MonitorPlay size={15} /><strong>{deviceName(device)}</strong><small>{device.client_ip}</small></span>)}</div> : <p className="protection-inline-empty">No observed devices currently fall back to Home.</p>; }
function deviceName(device: DeviceSummary) { return device.display_name || device.name || device.client_ip; }
function protectedDeviceLabel(count: number, isDefault: boolean) {
  if (count > 0) return `${count} currently covered`;
  return isDefault ? "Waiting for devices" : "Assign later";
}
function customRuleLabel(count: number) {
  if (count === 0) return "None";
  return `${count} custom rule${count === 1 ? "" : "s"}`;
}
function protectionDraft(protection: Protection): Draft { return { name: protection.name, icon: protection.icon, blocklist_ids: protection.blocklist_ids, allow_domains: protection.allow_entries.map((item) => item.domain), block_domains: protection.block_entries.map((item) => item.domain), device_ips: protection.device_ips, schedule: protection.schedule }; }
function cleanInput(draft: Draft): ProtectionInput { return { name: draft.name.trim(), icon: draft.icon, blocklist_ids: draft.blocklist_ids, allow_domains: draft.allow_domains.filter(Boolean), block_domains: draft.block_domains.filter(Boolean), device_ips: draft.device_ips, schedule: draft.schedule }; }
function lines(value: string) { return value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean); }
function toggleNumber(values: number[], value: number) { return values.includes(value) ? values.filter((item) => item !== value) : [...values, value]; }
function toggleString(values: string[], value: string) { return values.includes(value) ? values.filter((item) => item !== value) : [...values, value]; }
function toggleDay(days: string, day: number) { const values = days.split(",").filter(Boolean).map(Number); return (values.includes(day) ? values.filter((value) => value !== day) : [...values, day]).sort((a, b) => a - b).join(","); }
function pauseUntil(preset: "5m" | "1h" | "tomorrow" | "resume") { if (preset === "resume") return ""; const until = new Date(); if (preset === "5m") until.setMinutes(until.getMinutes() + 5); else if (preset === "1h") until.setHours(until.getHours() + 1); else { until.setDate(until.getDate() + 1); until.setHours(7, 0, 0, 0); } return until.toISOString(); }
function protectionStateLabel(protection: Protection) { if (protection.state === "paused") return `Domain blocking off until ${formatPauseTime(protection.paused_until)}`; if (protection.state === "scheduled_off") return "Domain blocking off · Outside schedule"; return protection.is_default ? "Network default · Blocking domains" : "Custom setup · Blocking domains"; }
function scheduleLabel(protection: Protection) { if (!protection.schedule.enabled) return "All day, every day"; return `Blocks ${protection.schedule.start}–${protection.schedule.end} · ${compactDays(protection.schedule.days)}`; }
function compactDays(days: string) { if (days === "1,2,3,4,5,6,7") return "Every day"; if (days === "1,2,3,4,5") return "Weekdays"; if (days === "6,7") return "Weekends"; return days.split(",").map((day) => ["", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"][Number(day)]).filter(Boolean).join(", "); }
function formatPauseTime(value?: string | null) { if (!value) return "later"; return new Intl.DateTimeFormat(undefined, { weekday: "short", hour: "numeric", minute: "2-digit" }).format(new Date(value)); }
function sourceHost(value: string) { try { return new URL(value).hostname.replace(/^www\./, ""); } catch { return "Custom source"; } }
