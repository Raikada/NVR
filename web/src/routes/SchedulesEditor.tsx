// Weekly recording-schedule editor — drag on a 7×24 grid to define
// time windows. Each cell represents one hour; selected cells are
// merged into [start..end] windows when persisted.
//
// Wire format: array of {day_of_week: 0..6, start: "HH:MM",
// end: "HH:MM"} — the recorder takes whole-hour boundaries today.

import { useEffect, useState } from 'react';
import { Btn, Card, SectionHeader } from '../components/primitives';
import {
  getPolicySchedules,
  putPolicySchedules,
} from '../lib/api';
import type { RecordingSchedule, ToastInput } from '../lib/types';

interface Props {
  policyID: string;
  addToast: (t: ToastInput) => void;
}

const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'] as const;

function makeEmptyGrid(): boolean[][] {
  return Array.from({ length: 7 }, () => Array(24).fill(false));
}

function pad2(n: number): string {
  return n < 10 ? `0${n}` : String(n);
}

function gridToSchedules(grid: boolean[][]): RecordingSchedule[] {
  const out: RecordingSchedule[] = [];
  for (let day = 0; day < 7; day++) {
    let runStart = -1;
    for (let h = 0; h < 24; h++) {
      const on = grid[day][h];
      if (on && runStart < 0) runStart = h;
      if ((!on || h === 23) && runStart >= 0) {
        const end = on ? 24 : h;
        out.push({
          day_of_week: day,
          start: `${pad2(runStart)}:00`,
          end: end === 24 ? '24:00' : `${pad2(end)}:00`,
        });
        runStart = -1;
      }
    }
  }
  return out;
}

function schedulesToGrid(schedules: RecordingSchedule[]): boolean[][] {
  const g = makeEmptyGrid();
  for (const s of schedules) {
    const sh = parseInt(s.start.split(':')[0] ?? '0', 10);
    const ehRaw = s.end.startsWith('24') ? 24 : parseInt(s.end.split(':')[0] ?? '0', 10);
    for (let h = sh; h < ehRaw; h++) {
      if (s.day_of_week >= 0 && s.day_of_week < 7 && h >= 0 && h < 24) {
        g[s.day_of_week][h] = true;
      }
    }
  }
  return g;
}

export function SchedulesEditor({ policyID, addToast }: Props) {
  const [grid, setGrid] = useState<boolean[][]>(makeEmptyGrid);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  // Drag state
  const [dragging, setDragging] = useState<null | { paint: boolean }>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    getPolicySchedules(policyID)
      .then((items) => {
        if (cancelled) return;
        setGrid(schedulesToGrid(items));
      })
      .catch((e: Error) => {
        if (cancelled) return;
        setError(e.message);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [policyID]);

  function setCell(day: number, hour: number, on: boolean) {
    setGrid((g) => {
      if (g[day][hour] === on) return g;
      const copy = g.map((row) => row.slice());
      copy[day][hour] = on;
      return copy;
    });
  }

  function onMouseDown(day: number, hour: number) {
    const target = !grid[day][hour];
    setDragging({ paint: target });
    setCell(day, hour, target);
  }

  function onMouseEnter(day: number, hour: number) {
    if (!dragging) return;
    setCell(day, hour, dragging.paint);
  }

  function onMouseUp() {
    setDragging(null);
  }

  async function save() {
    setSaving(true);
    try {
      await putPolicySchedules(policyID, gridToSchedules(grid));
      addToast({ kind: 'success', title: 'SCHEDULE SAVED', icon: 'check-circle' });
    } catch (e) {
      addToast({ kind: 'danger', title: 'SAVE FAILED', body: (e as Error).message, icon: 'x' });
    }
    setSaving(false);
  }

  function clearAll() {
    setGrid(makeEmptyGrid());
  }

  function selectAll() {
    setGrid(Array.from({ length: 7 }, () => Array(24).fill(true)));
  }

  return (
    <Card>
      <SectionHeader
        right={
          <div style={{ display: 'flex', gap: 6 }}>
            <Btn kind="ghost" size="sm" onClick={clearAll}>
              Clear
            </Btn>
            <Btn kind="ghost" size="sm" onClick={selectAll}>
              All
            </Btn>
            <Btn kind="primary" size="sm" disabled={saving} onClick={save}>
              {saving ? 'Saving…' : 'Save'}
            </Btn>
          </div>
        }
      >
        WEEKLY SCHEDULE
      </SectionHeader>
      {loading && (
        <div style={{ padding: 12, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>
          Loading…
        </div>
      )}
      {error && (
        <div style={{ padding: 12, fontFamily: 'var(--font-mono)', fontSize: 11, color: '#EF4444' }}>
          {error}
        </div>
      )}
      <div onMouseUp={onMouseUp} onMouseLeave={onMouseUp} style={{ marginTop: 10, userSelect: 'none' }}>
        {/* Hour header */}
        <div style={{ display: 'grid', gridTemplateColumns: '40px repeat(24, 1fr)', gap: 1, marginBottom: 2 }}>
          <span />
          {Array.from({ length: 24 }, (_, h) => (
            <span
              key={h}
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 9,
                color: 'var(--text-muted)',
                textAlign: 'center',
                letterSpacing: 0.5,
              }}
            >
              {h % 3 === 0 ? pad2(h) : ''}
            </span>
          ))}
        </div>
        {grid.map((row, day) => (
          <div
            key={day}
            style={{ display: 'grid', gridTemplateColumns: '40px repeat(24, 1fr)', gap: 1, marginBottom: 1 }}
          >
            <span
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                color: 'var(--text-secondary)',
                letterSpacing: 1,
                textTransform: 'uppercase',
                display: 'flex',
                alignItems: 'center',
              }}
            >
              {DAYS[day]}
            </span>
            {row.map((on, h) => (
              <div
                key={h}
                onMouseDown={() => onMouseDown(day, h)}
                onMouseEnter={() => onMouseEnter(day, h)}
                style={{
                  height: 22,
                  background: on ? '#F97316' : 'var(--bg-tertiary)',
                  border: '1px solid var(--border)',
                  cursor: 'pointer',
                  transition: 'background 80ms var(--ease-out)',
                }}
                title={`${DAYS[day]} ${pad2(h)}:00–${pad2(h + 1)}:00`}
              />
            ))}
          </div>
        ))}
      </div>
      <div
        style={{
          marginTop: 10,
          fontFamily: 'var(--font-mono)',
          fontSize: 10,
          color: 'var(--text-muted)',
        }}
      >
        Click and drag to paint hourly windows. Saves as one row per
        contiguous range.
      </div>
    </Card>
  );
}
