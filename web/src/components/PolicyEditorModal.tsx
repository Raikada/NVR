// PolicyEditorModal — create or edit a canonical RecordingPolicy.
//
// Surface for the new top-level Policies route AND the Camera drawer's
// inline "+ New policy" / "Edit policy" shortcuts. Renders as a fixed
// overlay + center-aligned panel (the same overlay/panel pattern the
// camera config drawer uses; we don't have a dedicated modal primitive
// and inventing one for a single use-site would be infra drift).
//
// Layout per the design language: tactical mono labels, segmented
// controls, sliders for buffer durations. The five canonical modes
// (continuous, motion, schedule, event_triggered, off) all show as
// segmented options; mode-conditional sections (schedule editor,
// pre/post-event sliders) appear/disappear based on selection.
//
// Engine internals (container, part_duration, max_part_size,
// min/max_segment_duration, record_path_template, enabled) live behind
// the AdvancedDisclosure so the common case (set name + mode +
// retention) stays uncluttered.
//
// Save calls createRecordingPolicy or updateRecordingPolicy depending
// on whether an existing policy was passed in; on success the parent
// receives the freshly-returned canonical policy via onSave.

import { useState } from 'react';
import type { CSSProperties, ReactNode } from 'react';
import {
  Btn,
  Input,
  SectionHeader,
  Segmented,
  SliderField,
  Toggle,
} from './primitives';
import { Icon } from './Icon';
import {
  ApiError,
  DEFAULT_RECORDING_POLICY_ID,
  createRecordingPolicy,
  updateRecordingPolicy,
} from '../lib/api';
import type {
  RecordingPolicy,
  RecordingPolicyContainer,
  RecordingPolicyMode,
  RecordingPolicyWriteBody,
  ScheduleWindow,
} from '../lib/api';
import {
  daysToNs,
  formatPartSize,
  minutesToNs,
  nsToDays,
  nsToMinutes,
  nsToSeconds,
  secondsToNs,
} from '../lib/duration';

interface PolicyEditorModalProps {
  policy?: RecordingPolicy; // undefined → create mode
  onSave: (policy: RecordingPolicy) => void;
  onCancel: () => void;
}

const MODE_OPTIONS: { value: RecordingPolicyMode; label: string }[] = [
  { value: 'continuous', label: 'CONTINUOUS' },
  { value: 'motion', label: 'MOTION' },
  { value: 'schedule', label: 'SCHEDULE' },
  { value: 'event_triggered', label: 'EVENT' },
  { value: 'off', label: 'OFF' },
];

const CONTAINER_OPTIONS: { value: RecordingPolicyContainer; label: string }[] = [
  { value: 'fmp4', label: 'fMP4' },
  { value: 'mpegts', label: 'MPEG-TS' },
];

interface FormState {
  name: string;
  mode: RecordingPolicyMode;
  retention_days: number;
  pre_event_seconds: number;
  post_event_seconds: number;
  schedule: { timezone: string; windows: ScheduleWindow[] };
  // Engine internals (Advanced)
  enabled: boolean;
  container: RecordingPolicyContainer;
  part_duration_seconds: number;
  max_part_size_mb: number;
  min_segment_minutes: number;
  max_segment_minutes: number;
  record_path_template: string;
}

function defaultsFor(policy?: RecordingPolicy): FormState {
  return {
    name: policy?.name ?? '',
    mode: policy?.mode ?? 'continuous',
    retention_days: policy ? Math.round(nsToDays(policy.retention_duration)) : 14,
    pre_event_seconds: policy?.pre_event_buffer ? Math.round(nsToSeconds(policy.pre_event_buffer)) : 5,
    post_event_seconds: policy?.post_event_buffer ? Math.round(nsToSeconds(policy.post_event_buffer)) : 10,
    schedule: policy?.schedule ?? {
      timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
      windows: [],
    },
    enabled: policy?.enabled ?? true,
    container: (policy?.container || 'fmp4') as RecordingPolicyContainer,
    part_duration_seconds: policy ? Math.max(1, Math.round(nsToSeconds(policy.part_duration))) : 1,
    max_part_size_mb: policy ? Math.max(1, Math.round(policy.max_part_size / (1024 * 1024))) : 50,
    min_segment_minutes: policy ? Math.round(nsToMinutes(policy.min_segment_duration)) : 1,
    max_segment_minutes: policy ? Math.round(nsToMinutes(policy.max_segment_duration)) : 10,
    record_path_template: policy?.record_path_template ?? '',
  };
}

function formToBody(form: FormState): RecordingPolicyWriteBody {
  const body: RecordingPolicyWriteBody = {
    name: form.name.trim(),
    mode: form.mode,
    retention_duration: daysToNs(form.retention_days),
    min_segment_duration: minutesToNs(form.min_segment_minutes),
    max_segment_duration: minutesToNs(form.max_segment_minutes),
    container: form.container,
    enabled: form.enabled,
    part_duration: secondsToNs(form.part_duration_seconds),
    max_part_size: form.max_part_size_mb * 1024 * 1024,
  };
  if (form.mode === 'schedule') {
    body.schedule = form.schedule;
  }
  if (form.mode === 'event_triggered' || form.mode === 'motion') {
    body.pre_event_buffer = secondsToNs(form.pre_event_seconds);
    body.post_event_buffer = secondsToNs(form.post_event_seconds);
  }
  if (form.record_path_template.trim()) {
    body.record_path_template = form.record_path_template.trim();
  }
  return body;
}

export function PolicyEditorModal({ policy, onSave, onCancel }: PolicyEditorModalProps) {
  const [form, setForm] = useState<FormState>(() => defaultsFor(policy));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const isEdit = Boolean(policy);
  const isDefault = policy?.id === DEFAULT_RECORDING_POLICY_ID;

  function patch(p: Partial<FormState>) {
    setForm((s) => ({ ...s, ...p }));
  }

  async function save() {
    setErr(null);
    if (!form.name.trim()) {
      setErr('Name is required');
      return;
    }
    if (form.mode === 'schedule' && form.schedule.windows.length === 0) {
      setErr('Schedule mode requires at least one window');
      return;
    }
    setBusy(true);
    try {
      const body = formToBody(form);
      const result = isEdit
        ? await updateRecordingPolicy(policy!.id, body)
        : await createRecordingPolicy(body);
      onSave(result);
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : (e as Error).message;
      setErr(msg);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div
        onClick={busy ? undefined : onCancel}
        style={{
          position: 'fixed',
          inset: 0,
          background: 'rgba(0,0,0,0.6)',
          backdropFilter: 'blur(2px)',
          zIndex: 100,
        }}
      />
      <div
        style={{
          position: 'fixed',
          top: '50%',
          left: '50%',
          transform: 'translate(-50%,-50%)',
          width: 640,
          maxWidth: '94vw',
          maxHeight: '92vh',
          background: 'var(--bg-secondary)',
          border: '1px solid var(--border)',
          borderRadius: 6,
          boxShadow: '0 24px 48px rgba(0,0,0,0.55)',
          zIndex: 101,
          display: 'flex',
          flexDirection: 'column',
        }}
      >
        {/* Header */}
        <div
          style={{
            padding: '14px 18px',
            borderBottom: '1px solid var(--border)',
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
          }}
        >
          <div>
            <div
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                color: 'var(--text-muted)',
                letterSpacing: 1,
                textTransform: 'uppercase',
              }}
            >
              {isEdit ? `POLICY · ${policy!.id.slice(0, 8)}` : 'NEW RECORDING POLICY'}
            </div>
            <div
              style={{
                fontFamily: 'var(--font-sans)',
                fontSize: 16,
                fontWeight: 600,
                color: 'var(--text-primary)',
                marginTop: 2,
              }}
            >
              {isEdit ? `Edit ${policy!.name}` : 'Create policy'}
            </div>
          </div>
          <button
            onClick={onCancel}
            disabled={busy}
            style={{
              background: 'transparent',
              border: '1px solid var(--border)',
              borderRadius: 4,
              width: 28,
              height: 28,
              cursor: busy ? 'not-allowed' : 'pointer',
              color: 'var(--text-secondary)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
          >
            <Icon name="x" style={{ width: 12, height: 12 }} />
          </button>
        </div>

        {/* Body */}
        <div style={{ flex: 1, overflowY: 'auto', padding: 18, display: 'flex', flexDirection: 'column', gap: 18 }}>
          {/* Name */}
          <Input
            label="POLICY NAME"
            value={form.name}
            onChange={(v) => patch({ name: v })}
            placeholder="e.g. Business Hours, 24/7, Loading Dock Only"
            disabled={isDefault}
          />

          {/* Mode */}
          <div>
            <SectionHeader>MODE</SectionHeader>
            <div style={{ marginTop: 8 }}>
              <Segmented<RecordingPolicyMode>
                options={MODE_OPTIONS}
                value={form.mode}
                onChange={(v) => patch({ mode: v })}
                size="sm"
              />
            </div>
            {form.mode === 'motion' && (
              <div
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 10,
                  color: 'var(--text-muted)',
                  letterSpacing: 0.5,
                  marginTop: 8,
                }}
              >
                MOTION DETECTION SUBSYSTEM IS PENDING — POLICIES SAVE BUT WON'T TRIGGER UNTIL IT LANDS.
              </div>
            )}
          </div>

          {/* Retention */}
          <div>
            <SectionHeader>RETENTION</SectionHeader>
            <div style={{ marginTop: 8 }}>
              <SliderField
                label={`${form.retention_days} DAYS · MINIMUM RETAIN BEFORE PRUNE`}
                value={form.retention_days}
                min={1}
                max={365}
                onChange={(v) => patch({ retention_days: v })}
              />
            </div>
          </div>

          {/* Schedule editor (only when mode = schedule) */}
          {form.mode === 'schedule' && (
            <ScheduleEditor
              schedule={form.schedule}
              onChange={(schedule) => patch({ schedule })}
            />
          )}

          {/* Event buffers (only for event_triggered / motion) */}
          {(form.mode === 'event_triggered' || form.mode === 'motion') && (
            <div>
              <SectionHeader>EVENT BUFFER</SectionHeader>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginTop: 8 }}>
                <SliderField
                  label={`PRE-EVENT · ${form.pre_event_seconds}s`}
                  value={form.pre_event_seconds}
                  min={0}
                  max={60}
                  onChange={(v) => patch({ pre_event_seconds: v })}
                />
                <SliderField
                  label={`POST-EVENT · ${form.post_event_seconds}s`}
                  value={form.post_event_seconds}
                  min={0}
                  max={120}
                  onChange={(v) => patch({ post_event_seconds: v })}
                />
              </div>
            </div>
          )}

          {/* Advanced */}
          <AdvancedDisclosure>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <SectionHeader>ENABLED</SectionHeader>
                <Toggle on={form.enabled} onChange={(v) => patch({ enabled: v })} />
              </div>
              <div>
                <div style={labelStyle}>CONTAINER</div>
                <Segmented<RecordingPolicyContainer>
                  options={CONTAINER_OPTIONS}
                  value={form.container}
                  onChange={(v) => patch({ container: v })}
                  size="sm"
                />
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                <SliderField
                  label={`PART DURATION · ${form.part_duration_seconds}s (fMP4)`}
                  value={form.part_duration_seconds}
                  min={1}
                  max={10}
                  onChange={(v) => patch({ part_duration_seconds: v })}
                />
                <SliderField
                  label={`MAX PART SIZE · ${formatPartSize(form.max_part_size_mb * 1024 * 1024)}`}
                  value={form.max_part_size_mb}
                  min={1}
                  max={500}
                  onChange={(v) => patch({ max_part_size_mb: v })}
                />
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                <SliderField
                  label={`MIN SEGMENT · ${form.min_segment_minutes}m`}
                  value={form.min_segment_minutes}
                  min={1}
                  max={60}
                  onChange={(v) => patch({ min_segment_minutes: v })}
                />
                <SliderField
                  label={`MAX SEGMENT · ${form.max_segment_minutes}m`}
                  value={form.max_segment_minutes}
                  min={1}
                  max={120}
                  onChange={(v) => patch({ max_segment_minutes: v })}
                />
              </div>
              <Input
                label="RECORD PATH TEMPLATE (optional)"
                value={form.record_path_template}
                onChange={(v) => patch({ record_path_template: v })}
                placeholder="e.g. %path/%Y-%m-%d/%H-%M-%S"
                mono
              />
            </div>
          </AdvancedDisclosure>

          {err && (
            <div
              style={{
                padding: '10px 12px',
                background: 'rgba(239,68,68,0.07)',
                border: '1px solid rgba(239,68,68,0.27)',
                borderRadius: 4,
                fontFamily: 'var(--font-mono)',
                fontSize: 11,
                color: '#EF4444',
                letterSpacing: 0.3,
              }}
            >
              {err}
            </div>
          )}
        </div>

        {/* Footer */}
        <div
          style={{
            padding: '12px 18px',
            borderTop: '1px solid var(--border)',
            display: 'flex',
            justifyContent: 'flex-end',
            gap: 8,
          }}
        >
          <Btn kind="ghost" onClick={onCancel} disabled={busy}>
            Cancel
          </Btn>
          <Btn kind="primary" icon="check" onClick={save} disabled={busy}>
            {busy ? 'Saving…' : isEdit ? 'Save Changes' : 'Create Policy'}
          </Btn>
        </div>
      </div>
    </>
  );
}

const labelStyle: CSSProperties = {
  fontFamily: 'var(--font-mono)',
  fontSize: 10,
  color: 'var(--text-secondary)',
  letterSpacing: 1,
  textTransform: 'uppercase',
  marginBottom: 6,
};

/* ---------- ScheduleEditor ---------- */

const DAYS: { v: string; l: string }[] = [
  { v: 'mon', l: 'MON' },
  { v: 'tue', l: 'TUE' },
  { v: 'wed', l: 'WED' },
  { v: 'thu', l: 'THU' },
  { v: 'fri', l: 'FRI' },
  { v: 'sat', l: 'SAT' },
  { v: 'sun', l: 'SUN' },
];

interface ScheduleEditorProps {
  schedule: { timezone: string; windows: ScheduleWindow[] };
  onChange: (next: { timezone: string; windows: ScheduleWindow[] }) => void;
}

function ScheduleEditor({ schedule, onChange }: ScheduleEditorProps) {
  function setTimezone(tz: string) {
    onChange({ ...schedule, timezone: tz });
  }
  function setWindow(i: number, w: ScheduleWindow) {
    onChange({ ...schedule, windows: schedule.windows.map((x, idx) => (idx === i ? w : x)) });
  }
  function addWindow() {
    onChange({
      ...schedule,
      windows: [...schedule.windows, { days: ['mon', 'tue', 'wed', 'thu', 'fri'], start: '09:00', end: '17:00' }],
    });
  }
  function removeWindow(i: number) {
    onChange({ ...schedule, windows: schedule.windows.filter((_, idx) => idx !== i) });
  }

  return (
    <div>
      <SectionHeader>SCHEDULE</SectionHeader>
      <div style={{ marginTop: 8 }}>
        <Input
          label="TIMEZONE"
          value={schedule.timezone}
          onChange={setTimezone}
          placeholder="e.g. UTC, America/Los_Angeles"
          mono
        />
      </div>
      <div style={{ marginTop: 12, display: 'flex', flexDirection: 'column', gap: 10 }}>
        {schedule.windows.length === 0 && (
          <div
            style={{
              padding: '14px 12px',
              background: 'var(--bg-tertiary)',
              border: '1px dashed var(--border)',
              borderRadius: 4,
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              color: 'var(--text-muted)',
              letterSpacing: 1,
              textAlign: 'center',
              textTransform: 'uppercase',
            }}
          >
            NO WINDOWS · ADD ONE BELOW
          </div>
        )}
        {schedule.windows.map((w, i) => (
          <div
            key={i}
            style={{
              padding: 12,
              background: 'var(--bg-tertiary)',
              border: '1px solid var(--border)',
              borderRadius: 4,
              display: 'flex',
              flexDirection: 'column',
              gap: 10,
            }}
          >
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
              {DAYS.map((d) => {
                const active = w.days.includes(d.v);
                return (
                  <button
                    key={d.v}
                    onClick={() => {
                      const days = active ? w.days.filter((x) => x !== d.v) : [...w.days, d.v];
                      setWindow(i, { ...w, days });
                    }}
                    style={{
                      padding: '4px 10px',
                      background: active ? 'rgba(249,115,22,0.13)' : 'var(--bg-primary)',
                      border: `1px solid ${active ? 'rgba(249,115,22,0.45)' : 'var(--border)'}`,
                      borderRadius: 3,
                      cursor: 'pointer',
                      fontFamily: 'var(--font-mono)',
                      fontSize: 10,
                      letterSpacing: 1,
                      color: active ? '#F97316' : 'var(--text-muted)',
                    }}
                  >
                    {d.l}
                  </button>
                );
              })}
            </div>
            <div style={{ display: 'flex', gap: 10, alignItems: 'flex-end' }}>
              <div style={{ flex: 1 }}>
                <div style={labelStyle}>START</div>
                <input
                  type="time"
                  value={w.start}
                  onChange={(e) => setWindow(i, { ...w, start: e.target.value })}
                  style={timeInputStyle}
                />
              </div>
              <div style={{ flex: 1 }}>
                <div style={labelStyle}>END</div>
                <input
                  type="time"
                  value={w.end}
                  onChange={(e) => setWindow(i, { ...w, end: e.target.value })}
                  style={timeInputStyle}
                />
              </div>
              <Btn kind="ghost" icon="trash-2" size="sm" onClick={() => removeWindow(i)}>
                Remove
              </Btn>
            </div>
          </div>
        ))}
        <Btn kind="secondary" icon="plus" size="sm" onClick={addWindow}>
          Add Window
        </Btn>
      </div>
    </div>
  );
}

const timeInputStyle: CSSProperties = {
  width: '100%',
  background: 'var(--bg-primary)',
  border: '1px solid var(--border)',
  borderRadius: 4,
  padding: '8px 10px',
  color: 'var(--text-primary)',
  fontFamily: 'var(--font-mono)',
  fontSize: 12,
  outline: 'none',
  colorScheme: 'dark',
};

/* ---------- AdvancedDisclosure ---------- */

interface AdvancedDisclosureProps {
  children: ReactNode;
}

function AdvancedDisclosure({ children }: AdvancedDisclosureProps) {
  const [open, setOpen] = useState(false);
  return (
    <div
      style={{
        border: '1px solid var(--border)',
        borderRadius: 4,
        background: 'var(--bg-primary)',
      }}
    >
      <button
        onClick={() => setOpen((v) => !v)}
        style={{
          width: '100%',
          padding: '10px 14px',
          background: 'transparent',
          border: 'none',
          cursor: 'pointer',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          color: 'var(--text-secondary)',
          fontFamily: 'var(--font-mono)',
          fontSize: 11,
          letterSpacing: 2,
          textTransform: 'uppercase',
        }}
      >
        ADVANCED
        <Icon
          name="chevron-down"
          style={{
            width: 14,
            height: 14,
            transform: open ? 'rotate(180deg)' : 'rotate(0deg)',
            transition: 'transform 150ms var(--ease-out)',
          }}
        />
      </button>
      {open && (
        <div style={{ padding: 14, borderTop: '1px solid var(--border)' }}>{children}</div>
      )}
    </div>
  );
}

