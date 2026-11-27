import { RadioCard, RadioGroup } from "../components/ui/radio-card";
import { DialogSurface } from "../components/DialogSurface";
import { Textarea } from "../components/ui/textarea";
import { Checkbox } from "../components/ui/checkbox";
import { Input } from "../components/ui/input";
import { Button } from "../components/ui/button";
import {
  Baby,
  BriefcaseBusiness,
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
import { useEffect, useMemo, useRef, useState, type ReactNode, type SubmitEvent } from "react";
import {
  api,
  type Blocklist,
  type DeviceSummary,
  type Protection,
  type ProtectionIconKey,
  type ProtectionInput
} from "../api/client";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { ProtectionRulePreview } from "../components/ProtectionRulePreview";
import { errorMessage, normalizeURL } from "../utils/formatting";
import { blocklistCatalog } from "../data/blocklists";

type Props = {
  readonly protections: Protection[];
  readonly blocklists: Blocklist[];
  readonly devices: DeviceSummary[];
  readonly refresh: () => Promise<void>;
  readonly onManageBlocklists: () => void;
};

type Draft = ProtectionInput;
type ProtectionLevel = "standard" | "stricter" | "custom";
type SourceDraft = { name: string; url: string };

const protectionLevels = [
  { id: "standard", name: "Standard", description: "Everyday ad and tracker blocking with compatibility first.", sourceID: "oisd-small" },
  { id: "stricter", name: "Stricter", description: "Broader privacy and telemetry blocking. Some apps may need exceptions.", sourceID: "hagezi-normal" },
  { id: "custom", name: "Custom", description: "Choose installed lists, add your own source, or use only domain exceptions.", sourceID: "" }
] as const;

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
  device_ids: [],
  schedule: { enabled: false, days: "1,2,3,4,5,6,7", start: "08:00", end: "22:00", timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC" }
});

export function ProtectionPage({ protections, blocklists, devices, refresh, onManageBlocklists }: Props) {
  const [selectedID, setSelectedID] = useState<number | null>(null);
  const [editor, setEditor] = useState<Draft>(emptyDraft);
  const [wizard, setWizard] = useState(false);
  const [step, setStep] = useState(0);
  const [draft, setDraft] = useState<Draft>(emptyDraft);
  const [level, setLevel] = useState<ProtectionLevel>("standard");
  const [customSource, setCustomSource] = useState<SourceDraft>({ name: "", url: "" });
  const [wizardBlocklists, setWizardBlocklists] = useState<Blocklist[]>(blocklists);
  const [creationStage, setCreationStage] = useState("");
  const [preparedSources, setPreparedSources] = useState<string[]>([]);
  const [createdProtection, setCreatedProtection] = useState(false);
  const pendingCreation = useRef<ProtectionInput | null>(null);
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

  const assignedIDs = useMemo(() => new Set(protections.flatMap((item) => item.is_default ? [] : item.device_ids)), [protections]);
  const homeDevices = devices.filter((device) => !assignedIDs.has(device.device_id));

  function openWizard() {
    setDraft(emptyDraft());
    setLevel("standard");
    setCustomSource({ name: "", url: "" });
    setWizardBlocklists(blocklists);
    setPreparedSources([]);
    setCreatedProtection(false);
    pendingCreation.current = null;
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
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      if (!createdProtection) {
        setCreationStage("Checking your protection choices");
        const existingProtections = await api.protections();
        const existing = existingProtections.find((item) => item.name.toLowerCase() === draft.name.trim().toLowerCase());
        if (existing && (!pendingCreation.current || !sameProtection(existing, pendingCreation.current))) {
          throw new Error("A protection with this name already exists. Go back and choose another name.");
        }
        if (!existing) {
          const installed = await api.blocklists();
          setWizardBlocklists(installed);
          let blocklistIDs = level === "custom" ? draft.blocklist_ids : [];
          if (blocklistIDs.some((id) => !installed.some((item) => item.id === id && item.enabled))) {
            throw new Error("A selected list is no longer active. Go back to Custom and update your selection.");
          }
          const source = creationSource(level, customSource);
          if (source) {
            const sourceID = await prepareSource(source, installed);
            blocklistIDs = Array.from(new Set([...blocklistIDs, sourceID]));
          }
          const input = cleanInput({ ...draft, blocklist_ids: blocklistIDs });
          pendingCreation.current = input;
          setCreationStage("Creating protection and applying DNS changes");
          await api.createProtection(input);
        }
        setCreatedProtection(true);
      }
      setCreationStage("Refreshing your protection setups");
      await refresh();
      setSelectedID(null);
      setWizard(false);
      setNotice(`${draft.name} is ready.`);
    } catch (error_) {
      setError(errorMessage(error_, "Could not create protection."));
    } finally {
      setCreationStage("");
      setBusy(false);
    }
  }

  async function prepareSource(source: SourceDraft, installed: Blocklist[]): Promise<number> {
    let existing = installed.find((item) => normalizeURL(item.url) === normalizeURL(source.url));
    if (!existing) {
      if (installed.some((item) => item.name.toLowerCase() === source.name.trim().toLowerCase())) {
        throw new Error(`A different source already uses the name “${source.name}”. Choose Custom to select it or use a different source name.`);
      }
      setCreationStage(`Installing ${source.name}`);
      const result = await api.createBlocklist({ ...source, enabled: true, assign_to_default: false });
      existing = { id: result.id, ...source, enabled: true, entry_count: 0, protection_count: 0 };
      installed.push(existing);
      setWizardBlocklists([...installed]);
      setPreparedSources((current) => [...new Set([...current, source.name])]);
    } else if (!existing.enabled) {
      if (existing.protection_count !== 0) {
        throw new Error(`${existing.name} is disabled and belongs to another protection setup. Enabling it would affect that setup too. Choose another option, or enable it in Blocklists before trying again.`);
      }
      setCreationStage(`Enabling ${existing.name}`);
      await api.updateBlocklist({ ...existing, enabled: true });
      existing.enabled = true;
      setWizardBlocklists([...installed]);
      setPreparedSources((current) => [...new Set([...current, existing!.name])]);
    }
    if (!existing.entry_count) {
      if (existing.protection_count !== 0) {
        throw new Error(`${existing.name} has no downloaded domains and is used by another setup. Refresh it in Blocklists after reviewing that setup, or choose a different option.`);
      }
      setCreationStage(`Downloading domains from ${existing.name}`);
      const result = await api.refreshBlocklist(existing.id);
      existing.entry_count = result.entry_count;
      setWizardBlocklists([...installed]);
      if (!result.entry_count) throw new Error(`${existing.name} returned no usable domains. Check its source or choose another option, then try again.`);
    }
    return existing.id;
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
      {(error || notice) && <output className={`protection-message ${error ? "error" : "success"}`}><span>{error || notice}</span><Button variant="ghost" size="icon" className="icon-button" type="button" aria-label="Dismiss" onClick={() => { setError(""); setNotice(""); }}><X size={15} /></Button></output>}

      {!selected ? <ProtectionOverview protections={protections} blocklists={blocklists} devices={devices} homeDevices={homeDevices} pauseBusyID={pauseBusyID} onPause={changePause} onManage={(id) => { setSelectedID(id); setError(""); setNotice(""); }} onCreate={openWizard} /> : <ProtectionEditor selected={selected} editor={editor} setEditor={setEditor} blocklists={blocklists} devices={devices} homeDevices={homeDevices} saveStage={saveStage} busy={busy} onBack={() => { setSelectedID(null); setError(""); }} onDelete={() => setConfirmDelete(true)} onManageBlocklists={onManageBlocklists} saveSelected={saveSelected} />}

      {wizard && <ProtectionWizard step={step} setStep={(next) => { setStep(next); setError(""); }} draft={draft} setDraft={(next) => { pendingCreation.current = null; setDraft(next); }} level={level} setLevel={(next) => { pendingCreation.current = null; setLevel(next); }} customSource={customSource} setCustomSource={(next) => { pendingCreation.current = null; setCustomSource(next); }} blocklists={wizardBlocklists} devices={devices} busy={busy} error={error} creationStage={creationStage} preparedSources={preparedSources} created={createdProtection} onClose={() => { if (!busy) { setWizard(false); setError(""); } }} onFinish={() => void finishWizard()} />}
      {confirmDelete && selected && !selected.is_default && <ConfirmDialog title={`Delete ${selected.name}?`} body="This protection setup and its exceptions will be permanently deleted." confirmLabel="Delete protection" busyLabel="Deleting protection…" busy={busy} onCancel={() => setConfirmDelete(false)} onConfirm={() => void removeSelected()} detail={<div className="confirm-dialog-impact warning"><Shield size={18} /><span><strong>{selected.device_ids.length} assigned device{selected.device_ids.length === 1 ? "" : "s"} will return to Home</strong><small>The devices themselves and their activity history will remain.</small></span></div>} />}
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
  const rules = draftRules(editor);
  const previewKey = JSON.stringify([selected.id, selected.updated_at, draftRules(protectionDraft(selected)), rules]);
  const defaultCopy = selected.is_default ? "Any device not assigned elsewhere uses Home." : "Only the devices selected below use these choices.";
  return <div className="protection-edit-view">
    <div className="protection-edit-nav"><Button variant="outline" className="secondary" type="button" disabled={Boolean(saveStage)} onClick={onBack}><ChevronLeft size={16} />All protection setups</Button><span><small>Editing setup</small><strong>{selected.name}</strong></span></div>
    <form className={`protection-editor panel ${saveStage ? "saving" : ""}`} aria-busy={Boolean(saveStage)} onSubmit={(event) => void saveSelected(event)}>
      <div className="protection-editor-heading"><div className="protection-title-icon"><ProtectionIcon name={editor.icon} /></div><div><span>{selected.is_default ? "Network default" : "Custom protection"}</span><Input value={editor.name} disabled={selected.is_default} maxLength={40} aria-label="Protection name" onChange={(event) => setEditor({ ...editor, name: event.target.value })} /><p>{defaultCopy}</p></div>{!selected.is_default && <Button variant="destructive" size="icon" className="icon-button" type="button" title="Delete protection" aria-label={`Delete ${selected.name}`} disabled={Boolean(saveStage)} onClick={onDelete}><Trash2 size={17} /></Button>}</div>
      <EditorSection title="Icon" description="Choose a simple visual cue."><IconPicker value={editor.icon} onChange={(icon) => setEditor({ ...editor, icon })} compact /></EditorSection>
      <EditorSection title="Blocking" description="Select installed lists for this protection setup."><div className="protection-choice-grid">{enabledBlocklists.map((blocklist) => <label key={blocklist.id} className={editor.blocklist_ids.includes(blocklist.id) ? "selected" : ""}><Checkbox  checked={editor.blocklist_ids.includes(blocklist.id)} onCheckedChange={() => setEditor({ ...editor, blocklist_ids: toggleNumber(editor.blocklist_ids, blocklist.id) })} /><span><strong>{blocklist.name}</strong><small>{blocklist.entry_count.toLocaleString()} domains</small></span></label>)}{enabledBlocklists.length === 0 && <p className="protection-inline-empty">No active blocklists are installed. Add one from the Blocklists page first.</p>}</div><Button variant="outline" className="secondary protection-add-list" type="button" onClick={onManageBlocklists}>Manage blocklists</Button></EditorSection>
      <EditorSection title="Exceptions" description="Use one exact domain per line. Allowed domains always win."><div className="protection-domain-editors"><label><span>Always allow</span><Textarea value={editor.allow_domains.join("\n")} onChange={(event) => setEditor({ ...editor, allow_domains: lines(event.target.value) })} placeholder="example.com" /></label><label><span>Always block</span><Textarea value={editor.block_domains.join("\n")} onChange={(event) => setEditor({ ...editor, block_domains: lines(event.target.value) })} placeholder="telemetry.example" /></label></div></EditorSection>
      <ProtectionRulePreview key={previewKey} id={selected.id} rules={rules} />
      <EditorSection title="Domain blocking schedule" description="Choose when Faro applies this setup’s blocklists and custom blocks. This schedule never disconnects devices."><ScheduleEditor draft={editor} setDraft={setEditor} /></EditorSection>
      <EditorSection title="Devices" description={selected.is_default ? "Home automatically covers every device not assigned elsewhere." : "Choose which observed devices use this protection."}>{selected.is_default ? <DeviceSummaryList devices={homeDevices} /> : <DeviceChoices devices={devices} selected={editor.device_ids} onChange={(deviceIPs) => setEditor({ ...editor, device_ids: deviceIPs })} />}</EditorSection>
      <footer className={`protection-save-bar ${saveStage ? "saving" : ""}`}><ProtectionSaveProgress saveStage={saveStage} selectedName={selected.name} busy={busy} editorName={editor.name} /></footer>
    </form>
  </div>;
}

function ProtectionSaveProgress({ saveStage, selectedName, busy, editorName }: { readonly saveStage: string; readonly selectedName: string; readonly busy: boolean; readonly editorName: string }) {
  return <>{saveStage ? <output className="protection-save-progress" aria-live="polite"><span className="protection-save-spinner"><LoaderCircle className="spinning" size={17} /></span><div><strong>Saving {selectedName}</strong><small>{saveStage}. Keep this page open.</small><i aria-hidden="true"><b /></i></div></output> : <span>Changes apply to DNS as soon as they are saved.</span>}<Button variant="default" type="submit" disabled={busy || !editorName.trim()}>{saveStage ? <LoaderCircle className="spinning" size={16} /> : <Save size={16} />}<span>{saveStage ? "Saving changes…" : "Save changes"}</span></Button></>;
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
          <Button variant="default" type="button" onClick={onCreate}><Plus size={16} />New protection setup</Button>
        </div>
      </div>

      <div className="protection-card-grid">
        {protections.map((protection) => {
          const installed = blocklists.filter((item) => protection.blocklist_ids.includes(item.id));
          const protectedDevices = protection.is_default ? homeDevices : devices.filter((device) => protection.device_ids.includes(device.device_id));
          const exceptionCount = protection.allow_entries.length + protection.block_entries.length;
          return (
            <article className={`protection-summary-card panel ${protection.is_default ? "default" : ""} ${protection.is_active ? "" : "inactive"}`} key={protection.id} aria-label={`${protection.name} protection`}>
              <div className="protection-card-top">
                <span className="protection-card-icon"><ProtectionIcon name={protection.icon} /></span>
                <div className="protection-card-copy"><div className="protection-card-title"><h2>{protection.name}</h2>{protection.is_default && <span className="protection-default-label">Network default</span>}</div><small className={`protection-state ${protection.is_active ? "active" : ""}`}>{protectionStateLabel(protection)}</small><p>{protection.is_default ? "Automatically protects every device that does not use another setup." : "Applies to the devices assigned to this setup."}</p></div>
                <Button variant="outline" className="secondary protection-card-manage" type="button" onClick={() => onManage(protection.id)}><Settings2 size={15} />Edit</Button>
              </div>
              <dl className="protection-card-facts">
                <div><dt><ListFilter size={15} />Blocklists</dt><dd title={installed.map((item) => item.name).join(", ")}>{installed.length ? `${installed.length} selected` : "None selected"}</dd></div>
                <div><dt><MonitorPlay size={15} />Devices</dt><dd>{protectedDeviceLabel(protectedDevices.length, protection.is_default)}</dd></div>
                <div><dt><Shield size={15} />Exceptions</dt><dd>{customRuleLabel(exceptionCount)}</dd></div>
                <div><dt><CalendarClock size={15} />Schedule</dt><dd>{scheduleLabel(protection)}</dd></div>
              </dl>
              <div className={`protection-temporary-control ${protection.state === "paused" ? "paused" : ""}`}>
                <span className="protection-temporary-icon">{protection.state === "paused" ? <Pause size={17} /> : <ShieldCheck size={17} />}</span>
                <div className="protection-temporary-copy"><strong>{protection.state === "paused" ? `Allowing all domains until ${formatPauseTime(protection.paused_until)}` : "Pause domain blocking"}</strong><p>Devices stay online. Faro {protection.state === "paused" ? "is allowing" : "will allow"} domains this setup would normally block.</p></div>
                <div className="protection-pause-actions" aria-label={`Temporarily stop domain blocking for ${protection.name}`}>
                  {protection.state === "paused" ? <Button variant="default" type="button" disabled={pauseBusyID === protection.id} onClick={() => void onPause(protection, "resume")}><Play size={14} />Start blocking again</Button> : <><Button variant="outline" className="secondary" type="button" disabled={pauseBusyID === protection.id} onClick={() => void onPause(protection, "5m")}>For 5 min</Button><Button variant="outline" className="secondary" type="button" disabled={pauseBusyID === protection.id} onClick={() => void onPause(protection, "1h")}>For 1 hour</Button><Button variant="outline" className="secondary" type="button" disabled={pauseBusyID === protection.id} onClick={() => void onPause(protection, "tomorrow")}>Until tomorrow</Button></>}
                </div>
              </div>
            </article>
          );
        })}
      </div>
    </section>
  );
}

function ProtectionWizard({ step, setStep, draft, setDraft, level, setLevel, customSource, setCustomSource, blocklists, devices, busy, error, creationStage, preparedSources, created, onClose, onFinish }: {
  readonly step: number;
  readonly setStep: (step: number) => void;
  readonly draft: Draft;
  readonly setDraft: (draft: Draft) => void;
  readonly level: ProtectionLevel;
  readonly setLevel: (level: ProtectionLevel) => void;
  readonly customSource: SourceDraft;
  readonly setCustomSource: (source: SourceDraft) => void;
  readonly blocklists: Blocklist[];
  readonly devices: DeviceSummary[];
  readonly busy: boolean;
  readonly error: string;
  readonly creationStage: string;
  readonly preparedSources: string[];
  readonly created: boolean;
  readonly onClose: () => void;
  readonly onFinish: () => void;
}) {
  const titles = ["Name and protection", "Choose devices", "Review and create"];
  const source = creationSource(level, customSource);
  const unavailableSource = source ? blocklists.find((item) => normalizeURL(item.url) === normalizeURL(source.url) && !item.enabled && (item.protection_count ?? 0) > 0) : undefined;
  const exceptionCount = draft.allow_domains.filter(Boolean).length + draft.block_domains.filter(Boolean).length;
  const customSourceError = level === "custom" ? sourceValidation(customSource) : "";
  const canContinue = step !== 0 || (draft.name.trim().length > 0 && !customSourceError && !unavailableSource && draft.blocklist_ids.length + (level === "custom" && source ? 1 : 0) <= 20);
  return (
    <DialogSurface onClose={onClose} busy={busy}  className="protection-wizard-backdrop" aria-labelledby="protection-wizard-title">
      <section className="protection-wizard protection-creation">
        <header><div><span>STEP {step + 1} OF {titles.length}</span><h2 id="protection-wizard-title">{titles[step]}</h2></div><Button variant="ghost" size="icon" className="icon-button" type="button" aria-label="Close" disabled={busy} onClick={onClose}><X size={19} /></Button></header>
        <div className="protection-step-track" aria-hidden="true">{titles.map((_, index) => <i key={index} className={index <= step ? "active" : ""} />)}</div>
        <div className="protection-wizard-body">
          {step === 0 && <>
            <p>Give this setup a name, then choose how it should block domains.</p>
            <label className="protection-name-field"><span>Name</span><Input autoFocus value={draft.name} maxLength={40} onChange={(event) => setDraft({ ...draft, name: event.target.value })} placeholder="Guests, Work, Living room…" /></label>
            <details className="protection-creation-options"><summary>Choose an icon</summary><IconPicker value={draft.icon} onChange={(icon) => setDraft({ ...draft, icon })} compact /></details>
            <RadioGroup className="protection-level-choices" aria-label="Protection level" value={level} onValueChange={(value) => setLevel(value as ProtectionLevel)}>
              {protectionLevels.map((choice) => <RadioCard value={choice.id} key={choice.id}><span><strong>{choice.name}{choice.id === "standard" && <em>Recommended</em>}</strong><small>{choice.description}</small><span>{choice.id === "custom" ? "Your own selection" : blocklistCatalog.find((item) => item.id === choice.sourceID)?.name}</span></span></RadioCard>)}
            </RadioGroup>
            {level !== "custom" && <p className="protection-source-note">Faro installs the required list when you create this setup. You can adjust its lists and exceptions later.</p>}
            {unavailableSource && <p className="protection-wizard-error" role="alert">{unavailableSource.name} is disabled and is used by another setup. Choose another option, or enable it in Blocklists after reviewing the affected setups.</p>}
            {level === "custom" && <CustomBlocking draft={draft} setDraft={setDraft} blocklists={blocklists} source={customSource} setSource={setCustomSource} sourceError={customSourceError} />}
          </>}
          {step === 1 && <>
            <p>Select the devices that should use {draft.name.trim()}. You can also assign them later.</p>
            <DeviceChoices devices={devices} selected={draft.device_ids} onChange={(deviceIPs) => setDraft({ ...draft, device_ids: deviceIPs })} />
            <details className="protection-creation-options"><summary>Domain exceptions <span>Optional{exceptionCount > 0 ? ` · ${exceptionCount} added` : ""}</span></summary><p>Use one exact domain per line. Allow exceptions take priority over blocks.</p><div className="protection-domain-editors"><label><span>Always allow</span><Textarea value={draft.allow_domains.join("\n")} onChange={(event) => setDraft({ ...draft, allow_domains: lines(event.target.value) })} placeholder="school.example" /></label><label><span>Always block</span><Textarea value={draft.block_domains.join("\n")} onChange={(event) => setDraft({ ...draft, block_domains: lines(event.target.value) })} placeholder="social.example" /></label></div></details>
          </>}
          {step === 2 && <Review draft={cleanInput(draft)} level={level} source={source} blocklists={blocklists} devices={devices} created={created} />}
          {creationStage && <output className="protection-creation-progress" aria-live="polite"><LoaderCircle className="spinning" size={17} />{creationStage}…</output>}
          {error && <div className="protection-wizard-error" role="alert"><p>{created ? `Protection was created, but its details could not refresh. ${error}` : error}</p><p>{created ? "Choose Refresh protection to finish." : "Your choices are saved here. Go back to change them, or choose Try again."}</p>{preparedSources.length > 0 && <p>Available in Blocklists: {preparedSources.join(", ")}. Faro will reuse these sources when you try again.</p>}</div>}
        </div>
        <footer><Button variant="outline" className="secondary" type="button" disabled={busy} onClick={() => step === 0 || created ? onClose() : setStep(step - 1)}><ChevronLeft size={16} />{created ? "Close" : step === 0 ? "Cancel" : "Back"}</Button>{step < 2 ? <Button variant="default" type="button" disabled={busy || !canContinue} onClick={() => setStep(step + 1)}>Continue<ChevronRight size={16} /></Button> : <Button variant="default" type="button" disabled={busy} onClick={onFinish}>{busy ? <LoaderCircle className="spinning" size={16} /> : <Shield size={16} />}{busy ? "Working…" : created ? "Refresh protection" : error ? "Try again" : "Create protection"}</Button>}</footer>
      </section>
    </DialogSurface>
  );
}

function CustomBlocking({ draft, setDraft, blocklists, source, setSource, sourceError }: { readonly draft: Draft; readonly setDraft: (draft: Draft) => void; readonly blocklists: Blocklist[]; readonly source: SourceDraft; readonly setSource: (source: SourceDraft) => void; readonly sourceError: string }) {
  const active = blocklists.filter((item) => item.enabled);
  return <section className="protection-custom-blocking" aria-label="Custom blocking">
    <h3>Installed lists</h3>
    {active.length > 0 ? <div className="protection-choice-grid">{active.map((item) => <label key={item.id} className={draft.blocklist_ids.includes(item.id) ? "selected" : ""}><Checkbox checked={draft.blocklist_ids.includes(item.id)} disabled={!draft.blocklist_ids.includes(item.id) && draft.blocklist_ids.length >= 20} onCheckedChange={() => setDraft({ ...draft, blocklist_ids: toggleNumber(draft.blocklist_ids, item.id) })} /><span><strong>{item.name}</strong><small>{item.entry_count.toLocaleString()} domains · {sourceHost(item.url)}</small></span></label>)}</div> : <p>No active lists yet. Add a source below or use domain exceptions in the next step.</p>}
    <details className="protection-creation-options"><summary>Add a custom source <span>Optional</span></summary><p>Use a public hosts file or domain list. Faro downloads it when you create this setup.</p><div className="protection-custom-source"><label><span>Source name</span><Input value={source.name} onChange={(event) => setSource({ ...source, name: event.target.value })} placeholder="My blocklist" /></label><label><span>Source URL</span><Input type="url" value={source.url} onChange={(event) => setSource({ ...source, url: event.target.value })} placeholder="https://example.com/domains.txt" /></label></div>{sourceError && <p className="protection-source-validation" role="status">{sourceError}</p>}</details>
    {draft.blocklist_ids.length >= 20 && source.url.trim() && <p className="protection-source-validation">Choose at most 20 lists, including your custom source.</p>}
  </section>;
}

function Review({ draft, level, source, blocklists, devices, created }: { readonly draft: Draft; readonly level: ProtectionLevel; readonly source: SourceDraft | null; readonly blocklists: Blocklist[]; readonly devices: DeviceSummary[]; readonly created: boolean }) {
  const installedNames = level === "custom" ? blocklists.filter((item) => draft.blocklist_ids.includes(item.id)).map((item) => item.name) : [];
  if (source) installedNames.push(source.name);
  const needsInstall = source && !blocklists.some((item) => normalizeURL(item.url) === normalizeURL(source.url));
  return <><div className="protection-review"><div className="protection-review-identity"><ProtectionIcon name={draft.icon} /><span><strong>{draft.name}</strong><small>{created ? "Created" : "Ready to create"}</small></span></div><ReviewRow label="Protection" value={protectionLevels.find((item) => item.id === level)?.name ?? "Custom"} /><ReviewRow label="Blocklists" value={[...new Set(installedNames)].join(", ") || "None — domain exceptions only"} />{needsInstall && <ReviewRow label="Install on create" value={source.name} />}<ReviewRow label="Always allow" value={`${draft.allow_domains.length} domain${draft.allow_domains.length === 1 ? "" : "s"}`} /><ReviewRow label="Always block" value={`${draft.block_domains.length} domain${draft.block_domains.length === 1 ? "" : "s"}`} /><ReviewRow label="Devices" value={draft.device_ids.length ? devices.filter((device) => draft.device_ids.includes(device.device_id)).map(deviceName).join(", ") : "None yet — assign later"} /><ReviewRow label="Schedule" value="Block domains all day, every day" /></div><p className="protection-source-note">{draft.device_ids.length ? "Selected devices will move to this protection when you create it." : "Assign devices later from Protection or Devices."} You can change the schedule after creating this setup.</p></>;
}

function ReviewRow({ label, value }: { readonly label: string; readonly value: string }) { return <div className="protection-review-row"><span>{label}</span><strong>{value}</strong></div>; }
function EditorSection({ title, description, children }: { readonly title: string; readonly description: string; readonly children: ReactNode }) { return <section className="protection-editor-section"><header><h3>{title}</h3><p>{description}</p></header><div>{children}</div></section>; }

function ScheduleEditor({ draft, setDraft }: { readonly draft: Draft; readonly setDraft: (draft: Draft) => void }) {
  const dayOptions = [[1, "M"], [2, "T"], [3, "W"], [4, "T"], [5, "F"], [6, "S"], [7, "S"]] as const;
  const selectedDays = draft.schedule.days.split(",").filter(Boolean).map(Number);
  const updateSchedule = (change: Partial<Draft["schedule"]>) => setDraft({ ...draft, schedule: { ...draft.schedule, ...change } });
  return <div className="protection-schedule-editor">
    <RadioGroup className="protection-schedule-modes" aria-label="When to block domains" value={draft.schedule.enabled ? "scheduled" : "always"} onValueChange={(value) => updateSchedule(value === "always" ? { enabled: false } : { enabled: true, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC", ...(draft.schedule.start === "00:00" && draft.schedule.end === "23:59" ? { start: "08:00", end: "22:00" } : {}) })}>
      <RadioCard value="always"><ShieldCheck size={18} /><span><strong>Block domains all the time</strong><small>Blocklists and custom rules apply every day, all day.</small></span></RadioCard>
      <RadioCard value="scheduled"><CalendarClock size={18} /><span><strong>Block only during selected hours</strong><small>Outside those hours, devices stay online and blocked domains are allowed.</small></span></RadioCard>
    </RadioGroup>
    {draft.schedule.enabled && <div className="protection-schedule-fields">
      <div><span className="protection-schedule-field-label">Blocking days</span><div className="protection-schedule-days" aria-label="Days when domain blocking is active">{dayOptions.map(([day, label]) => <button type="button" className={selectedDays.includes(day) ? "selected" : ""} aria-pressed={selectedDays.includes(day)} aria-label={["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"][day - 1]} key={day} onClick={() => updateSchedule({ days: toggleDay(draft.schedule.days, day) })}>{label}</button>)}</div></div>
      <div className="protection-schedule-times"><label><span>Blocking starts</span><Input type="time" value={draft.schedule.start} onChange={(event) => updateSchedule({ start: event.target.value })} /></label><span>to</span><label><span>Blocking ends</span><Input type="time" value={draft.schedule.end} onChange={(event) => updateSchedule({ end: event.target.value })} /></label></div>
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

function DeviceChoices({ devices, selected, onChange }: { readonly devices: DeviceSummary[]; readonly selected: number[]; readonly onChange: (selected: number[]) => void }) {
  if (!devices.length) return <p className="protection-inline-empty">No devices have been observed yet. You can assign devices later.</p>;
  return <div className="protection-device-choices">{devices.map((device) => <label className={selected.includes(device.device_id) ? "selected" : ""} key={device.device_id}><Checkbox  checked={selected.includes(device.device_id)} onCheckedChange={() => onChange(toggleNumber(selected, device.device_id))} /><MonitorPlay size={17} aria-hidden="true" /><span className="protection-device-copy"><strong>{deviceName(device)}</strong><small>{device.client_ip || "No current address"}{device.protection ? ` · currently ${device.protection}` : ""}</small></span></label>)}</div>;
}

function DeviceSummaryList({ devices }: { readonly devices: DeviceSummary[] }) { return devices.length ? <div className="protection-device-summary">{devices.map((device) => <span key={device.device_id}><MonitorPlay size={15} /><strong>{deviceName(device)}</strong><small>{device.client_ip}</small></span>)}</div> : <p className="protection-inline-empty">No observed devices currently fall back to Home.</p>; }
function deviceName(device: DeviceSummary) { return device.display_name || device.name || device.client_ip; }
function protectedDeviceLabel(count: number, isDefault: boolean) {
  if (count > 0) return `${count} currently covered`;
  return isDefault ? "Waiting for devices" : "Assign later";
}
function customRuleLabel(count: number) {
  if (count === 0) return "None";
  return `${count} custom rule${count === 1 ? "" : "s"}`;
}
function protectionDraft(protection: Protection): Draft { return { name: protection.name, icon: protection.icon, blocklist_ids: protection.blocklist_ids, allow_domains: protection.allow_entries.map((item) => item.domain), block_domains: protection.block_entries.map((item) => item.domain), device_ips: [], device_ids: protection.device_ids, schedule: protection.schedule }; }
function draftRules(draft: Draft) {
  return { blocklist_ids: draft.blocklist_ids, allow_domains: draft.allow_domains.filter(Boolean), block_domains: draft.block_domains.filter(Boolean) };
}
function cleanInput(draft: Draft): ProtectionInput { return { ...draft, ...draftRules(draft), name: draft.name.trim() }; }
function lines(value: string) { return value.split(/\r?\n/).map((line) => line.trim()); }
function toggleNumber(values: number[], value: number) { return values.includes(value) ? values.filter((item) => item !== value) : [...values, value]; }

function toggleDay(days: string, day: number) { const values = days.split(",").filter(Boolean).map(Number); return (values.includes(day) ? values.filter((value) => value !== day) : [...values, day]).sort((a, b) => a - b).join(","); }
function pauseUntil(preset: "5m" | "1h" | "tomorrow" | "resume") { if (preset === "resume") return ""; const until = new Date(); if (preset === "5m") until.setMinutes(until.getMinutes() + 5); else if (preset === "1h") until.setHours(until.getHours() + 1); else { until.setDate(until.getDate() + 1); until.setHours(7, 0, 0, 0); } return until.toISOString(); }
function protectionStateLabel(protection: Protection) { if (protection.state === "paused") return `Domain blocking off until ${formatPauseTime(protection.paused_until)}`; if (protection.state === "scheduled_off") return "Domain blocking off · Outside schedule"; return "Blocking domains"; }
function scheduleLabel(protection: Protection) { if (!protection.schedule.enabled) return "All day, every day"; return `Blocks ${protection.schedule.start}–${protection.schedule.end} · ${compactDays(protection.schedule.days)}`; }
function compactDays(days: string) { if (days === "1,2,3,4,5,6,7") return "Every day"; if (days === "1,2,3,4,5") return "Weekdays"; if (days === "6,7") return "Weekends"; return days.split(",").map((day) => ["", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"][Number(day)]).filter(Boolean).join(", "); }
function formatPauseTime(value?: string | null) { if (!value) return "later"; return new Intl.DateTimeFormat(undefined, { weekday: "short", hour: "numeric", minute: "2-digit" }).format(new Date(value)); }
function sourceHost(value: string) { try { return new URL(value).hostname.replace(/^www\./, ""); } catch { return "Custom source"; } }

function creationSource(level: ProtectionLevel, customSource: SourceDraft): SourceDraft | null {
  if (level === "custom") return customSource.url.trim() ? { name: customSource.name.trim(), url: customSource.url.trim() } : null;
  const sourceID = protectionLevels.find((item) => item.id === level)?.sourceID;
  const source = blocklistCatalog.find((item) => item.id === sourceID);
  return source ? { name: source.name, url: source.url } : null;
}

function sourceValidation(source: SourceDraft): string {
  if (!source.name.trim() && !source.url.trim()) return "";
  if (!source.name.trim()) return "Give your custom source a name.";
  try {
    const url = new URL(source.url.trim());
    if (url.username || url.password) return "Use a source URL without a username or password.";
    if (url.protocol === "https:" || url.protocol === "http:") return "";
  } catch { /* The editable source is incomplete. */ }
  return "Enter a complete http:// or https:// source URL.";
}

function sameProtection(protection: Protection, input: ProtectionInput): boolean {
  const domains = (values: string[]) => [...new Set(values.map((value) => value.trim().toLowerCase().replace(/\.$/, "")))].sort();
  const comparable = (value: ProtectionInput) => JSON.stringify({
    name: value.name.trim(),
    icon: value.icon,
    blocklist_ids: [...new Set(value.blocklist_ids)].sort((a, b) => a - b),
    allow_domains: domains(value.allow_domains),
    block_domains: domains(value.block_domains),
    device_ids: [...new Set(value.device_ids)].sort(),
    schedule: { enabled: value.schedule.enabled, days: value.schedule.days, start: value.schedule.start, end: value.schedule.end, timezone: value.schedule.timezone }
  });
  return comparable(protectionDraft(protection)) === comparable(input);
}
