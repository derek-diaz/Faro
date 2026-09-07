import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { NativeSelect } from "./ui/native-select";
import { Popover, PopoverTrigger, PopoverContent, PopoverClose } from "./ui/popover";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "./ui/tabs";
import { CalendarDays, ChevronDown, X } from "lucide-react";
import { useState } from "react";

export type ActivityTimeRangePreset = "all" | "15m" | "1h" | "4h" | "24h" | "7d" | "30d" | "90d" | "custom";

export type ActivityTimeRange = {
  readonly preset: ActivityTimeRangePreset;
  readonly from?: string;
  readonly to?: string;
};

type PickerView = "relative" | "absolute" | "now";
type RelativeUnit = "minutes" | "hours" | "days" | "weeks";

type ActivityTimePickerProps = {
  readonly value: ActivityTimeRange;
  readonly onChange: (value: ActivityTimeRange) => void;
};

type CommonRange = {
  readonly label: string;
  readonly getRange: () => ActivityTimeRange;
};

const MAX_RANGE_MS = 90 * 24 * 60 * 60 * 1000;
const relativeUnits: ReadonlyArray<{ readonly value: RelativeUnit; readonly label: string; readonly milliseconds: number }> = [
  { value: "minutes", label: "minutes", milliseconds: 60 * 1000 },
  { value: "hours", label: "hours", milliseconds: 60 * 60 * 1000 },
  { value: "days", label: "days", milliseconds: 24 * 60 * 60 * 1000 },
  { value: "weeks", label: "weeks", milliseconds: 7 * 24 * 60 * 60 * 1000 }
];

const commonRanges: ReadonlyArray<CommonRange> = [
  { label: "Today", getRange: () => calendarRange("today") },
  { label: "This week", getRange: () => calendarRange("week") },
  { label: "Last 15 minutes", getRange: () => ({ preset: "15m" }) },
  { label: "Last 24 hours", getRange: () => ({ preset: "24h" }) },
  { label: "Last 30 minutes", getRange: () => relativeRange(30, "minutes") },
  { label: "Last 7 days", getRange: () => ({ preset: "7d" }) },
  { label: "Last 1 hour", getRange: () => ({ preset: "1h" }) },
  { label: "Last 30 days", getRange: () => ({ preset: "30d" }) },
  { label: "Last 4 hours", getRange: () => ({ preset: "4h" }) },
  { label: "Last 90 days", getRange: () => ({ preset: "90d" }) }
];

const presetDurations: ReadonlyArray<{ readonly preset: ActivityTimeRangePreset; readonly milliseconds: number }> = [
  { preset: "15m", milliseconds: 15 * 60 * 1000 },
  { preset: "1h", milliseconds: 60 * 60 * 1000 },
  { preset: "4h", milliseconds: 4 * 60 * 60 * 1000 },
  { preset: "24h", milliseconds: 24 * 60 * 60 * 1000 },
  { preset: "7d", milliseconds: 7 * 24 * 60 * 60 * 1000 },
  { preset: "30d", milliseconds: 30 * 24 * 60 * 60 * 1000 },
  { preset: "90d", milliseconds: 90 * 24 * 60 * 60 * 1000 }
];

export function ActivityTimePicker({ value, onChange }: ActivityTimePickerProps) {
  const [open, setOpen] = useState(false);
  const [view, setView] = useState<PickerView>("relative");
  const [relativeAmount, setRelativeAmount] = useState("30");
  const [relativeUnit, setRelativeUnit] = useState<RelativeUnit>("days");
  const [absoluteFrom, setAbsoluteFrom] = useState("");
  const [absoluteTo, setAbsoluteTo] = useState("");
  const [error, setError] = useState("");

  function showPicker(nextView: PickerView = "relative") {
    setError("");
    setView(nextView);
    if (nextView === "relative") {
      const relativeDefault = relativeEditorForRange(value);
      setRelativeAmount(relativeDefault.amount);
      setRelativeUnit(relativeDefault.unit);
    }
    if (nextView === "absolute") {
      const range = rangeForAbsoluteEditor(value);
      setAbsoluteFrom(toLocalDateTimeValue(range.from));
      setAbsoluteTo(toLocalDateTimeValue(range.to));
    }
    setOpen(true);
  }

  function applyRange(nextRange: ActivityTimeRange) {
    setError("");
    onChange(nextRange);
    setOpen(false);
  }

  function applyRelative() {
    const amount = Number(relativeAmount);
    const unit = relativeUnits.find((option) => option.value === relativeUnit);
    if (!unit || !Number.isFinite(amount) || amount < 1 || !Number.isInteger(amount)) {
      setError("Enter a whole number greater than zero.");
      return;
    }
    const duration = amount * unit.milliseconds;
    if (duration > MAX_RANGE_MS) {
      setError("Ranges can be up to 90 days.");
      return;
    }
    const preset = presetDurations.find((option) => option.milliseconds === duration)?.preset;
    if (preset) applyRange({ preset });
    else applyRange(relativeRange(amount, relativeUnit));
  }

  function applyAbsolute() {
    const from = new Date(absoluteFrom);
    const to = new Date(absoluteTo);
    const span = to.getTime() - from.getTime();
    if (Number.isNaN(from.getTime()) || Number.isNaN(to.getTime()) || span <= 0) {
      setError("Choose a valid start and end.");
      return;
    }
    if (span > MAX_RANGE_MS) {
      setError("Ranges can be up to 90 days.");
      return;
    }
    applyRange({ preset: "custom", from: from.toISOString(), to: to.toISOString() });
  }

  function applyNow() {
    const currentRange = rangeForAbsoluteEditor(value);
    const duration = Math.min(MAX_RANGE_MS, Math.max(60 * 1000, currentRange.to.getTime() - currentRange.from.getTime()));
    const to = new Date();
    const from = new Date(to.getTime() - duration);
    const preset = presetDurations.find((option) => option.milliseconds === duration)?.preset;
    if (preset) applyRange({ preset });
    else applyRange({ preset: "custom", from: from.toISOString(), to: to.toISOString() });
  }

  return (
    <Popover open={open} onOpenChange={(nextOpen) => { if (nextOpen) showPicker(); else setOpen(false); }}>
    <div className="activity-time-picker">
      <div className="activity-time-picker-bar">
        <PopoverTrigger render={<Button variant="outline" />} className="activity-time-picker-trigger">
          <CalendarDays size={15} aria-hidden="true" />
          <span>{activityTimeRangeLabel(value)}</span>
          <ChevronDown size={14} aria-hidden="true" />
        </PopoverTrigger>
      </div>
        <PopoverContent className="activity-range-popover" aria-label="Activity time range">
          <div className="flex items-center justify-between gap-3 mb-3">
            <strong className="text-sm">Activity time range</strong>
            <PopoverClose render={<Button variant="ghost" size="icon" />} aria-label="Close time range picker"><X size={15} /></PopoverClose>
          </div>
          <Tabs value={view} onValueChange={(next) => { if (next === "absolute") showPicker("absolute"); else { setView(next as PickerView); setError(""); } }}>
          <TabsList className="mb-4" aria-label="Time range mode">
            <TabsTrigger value="absolute">Absolute</TabsTrigger>
            <TabsTrigger value="relative">Relative</TabsTrigger>
            <TabsTrigger value="now">Now</TabsTrigger>
          </TabsList>

            <TabsContent value="relative">
              <div className="activity-time-popover-heading">
                <strong>Quick select</strong>
              </div>
              <div className="activity-relative-form">
                <NativeSelect aria-label="Relative range direction" value="last" disabled><option value="last">Last</option></NativeSelect>
                <Input aria-label="Relative range amount" min="1" type="number" value={relativeAmount} onChange={(event) => setRelativeAmount(event.target.value)} />
                <NativeSelect aria-label="Relative range unit" value={relativeUnit} onChange={(event) => setRelativeUnit(event.target.value as RelativeUnit)}>
                  {relativeUnits.map((unit) => <option key={unit.value} value={unit.value}>{unit.label}</option>)}
                </NativeSelect>
                <Button variant="default" type="button" onClick={applyRelative}>Apply</Button>
              </div>
              <div className="activity-common-ranges">
                <strong>Commonly used</strong>
                <div>
                  {commonRanges.map((range) => <Button variant="ghost" className="justify-start" key={range.label} type="button" onClick={() => applyRange(range.getRange())}>{range.label}</Button>)}
                </div>
              </div>
            </TabsContent>

            <TabsContent value="absolute" className="activity-absolute-form">
              <div className="activity-time-popover-heading"><strong>Absolute range</strong></div>
              <label>Start<Input type="datetime-local" value={absoluteFrom} onChange={(event) => setAbsoluteFrom(event.target.value)} /></label>
              <label>End<Input type="datetime-local" value={absoluteTo} onChange={(event) => setAbsoluteTo(event.target.value)} /></label>
              <div className="activity-absolute-actions"><Button variant="outline" className="secondary" type="button" onClick={() => setView("relative")}>Cancel</Button><Button variant="default" type="button" onClick={applyAbsolute}>Update</Button></div>
            </TabsContent>

            <TabsContent value="now" className="activity-now-panel">
              <div className="activity-now-heading"><span>Current time</span><strong>{formatPickerDate(new Date().toISOString())}</strong></div>
              <p>Move the end of this range to the current time while keeping its duration.</p>
              <div className="activity-now-actions"><Button variant="outline" className="secondary" type="button" onClick={() => setView("relative")}>Cancel</Button><Button variant="default" type="button" onClick={applyNow}>Update to now</Button></div>
            </TabsContent>
          </Tabs>

          {error && <p className="activity-range-error" role="alert">{error}</p>}
        </PopoverContent>
    </div>
    </Popover>
  );
}

export function activityTimeRangeLabel(range: ActivityTimeRange) {
  if (range.preset === "custom" && range.from && range.to) {
    return `${formatPickerDate(range.from)} → ${formatPickerDate(range.to)}`;
  }
  const labels: Record<ActivityTimeRangePreset, string> = {
    all: "All time",
    "15m": "Last 15 minutes",
    "1h": "Last 1 hour",
    "4h": "Last 4 hours",
    "24h": "Last 24 hours",
    "7d": "Last 7 days",
    "30d": "Last 30 days",
    "90d": "Last 90 days",
    custom: "Custom range"
  };
  return labels[range.preset];
}

function relativeRange(amount: number, unit: RelativeUnit): ActivityTimeRange {
  const to = new Date();
  const milliseconds = relativeUnits.find((option) => option.value === unit)?.milliseconds ?? 0;
  const from = new Date(to.getTime() - amount * milliseconds);
  return { preset: "custom", from: from.toISOString(), to: to.toISOString() };
}

function calendarRange(kind: "today" | "week"): ActivityTimeRange {
  const to = new Date();
  const from = new Date(to);
  from.setHours(0, 0, 0, 0);
  if (kind === "week") {
    const daysSinceMonday = (from.getDay() + 6) % 7;
    from.setDate(from.getDate() - daysSinceMonday);
  }
  return { preset: "custom", from: from.toISOString(), to: to.toISOString() };
}

function rangeForAbsoluteEditor(range: ActivityTimeRange) {
  const to = range.to ? new Date(range.to) : new Date();
  if (range.from) return { from: new Date(range.from), to };
  const milliseconds = presetDurations.find((option) => option.preset === range.preset)?.milliseconds ?? 24 * 60 * 60 * 1000;
  return { from: new Date(to.getTime() - milliseconds), to };
}

function relativeEditorForRange(range: ActivityTimeRange) {
  const defaults: Partial<Record<ActivityTimeRangePreset, { readonly amount: string; readonly unit: RelativeUnit }>> = {
    "15m": { amount: "15", unit: "minutes" },
    "1h": { amount: "1", unit: "hours" },
    "4h": { amount: "4", unit: "hours" },
    "24h": { amount: "24", unit: "hours" },
    "7d": { amount: "7", unit: "days" },
    "30d": { amount: "30", unit: "days" },
    "90d": { amount: "90", unit: "days" }
  };
  return defaults[range.preset] ?? { amount: "30", unit: "days" };
}

function toLocalDateTimeValue(date: Date) {
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function formatPickerDate(timestamp: string) {
  return new Date(timestamp).toLocaleString([], { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}
