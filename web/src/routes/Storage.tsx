// Storage route — disk overview, breakdown bar, RAID disk list.
// Wired to /v1/storage-volumes for capacity / used / status / mount
// path; per-content-type breakdown (Wave 5, 2026-05-06 amendment)
// renders /v1/storage-volumes/breakdown bytes-per-content-type and
// per-camera drilldown. Drive vendor-model strings + SMART hours are
// surfaced live when the host has smartmontools installed.

import { useState } from 'react';
import {
  Card,
  Progress,
  SectionHeader,
  Stat,
  StatusBadge,
} from '../components/primitives';
import type { StatusBadgeKind } from '../components/primitives';
import { PageHeader } from '../components/PageHeader';
import {
  fetchCameras,
  fetchRecordingPolicies,
  fetchStorageBreakdown,
  fetchStorageVolumes,
} from '../lib/api';
import type {
  RecordingSegmentContentType,
  StorageBreakdownByCamera,
  StorageBreakdownEntry,
  StorageVolume,
} from '../lib/api';
import { usePoll, formatBytes } from '../lib/hooks';
import { formatDuration } from '../lib/duration';

// Visual identity per content-type: chosen to match the existing
// orange/teal/blue palette already used elsewhere in the SPA. The
// recorder doesn't enforce a brand; these are the defaults.
const CONTENT_TYPE_COLORS: Record<RecordingSegmentContentType, string> = {
  continuous: '#F97316',
  motion: '#0EA5E9',
  scheduled: '#A78BFA',
  event_triggered: '#34D399',
  off: '#64748B',
};

const CONTENT_TYPE_LABELS: Record<RecordingSegmentContentType, string> = {
  continuous: 'Continuous',
  motion: 'Motion',
  scheduled: 'Scheduled',
  event_triggered: 'Event-triggered',
  off: 'Off',
};

function statusBadge(s: StorageVolume['status']): StatusBadgeKind {
  if (s === 'healthy') return 'online';
  if (s === 'degraded') return 'degraded';
  if (s === 'full' || s === 'read_only') return 'error';
  if (s === 'missing') return 'offline';
  return 'offline';
}

export function Storage() {
  // Poll cadence: 10s. Faster than the previous 30s so write_bytes_per_second
  // updates feel live; the recorder-side observer keys on the request
  // interval, so polling faster gives a more responsive (though
  // smaller-window) read.
  const volumes = usePoll(fetchStorageVolumes, 10_000, []);
  // Wave 5: per-content-type rollup. 30s cadence — the underlying
  // population (synthesized RecordingSegment metadata) only changes
  // as new segments are sealed; the recorder caches synthesis between
  // reloads and fast polling here would just rebuild the same map.
  const breakdown = usePoll(fetchStorageBreakdown, 30_000, []);
  // Camera list for the per-camera drilldown — gives us the
  // human-readable camera_name to display next to camera_id.
  const cameras = usePoll(() => fetchCameras(0, 100), 30_000, []);
  const [drilldownOpen, setDrilldownOpen] = useState(false);
  // Volumes whose backing disappeared (status: missing) shouldn't
  // count toward used / capacity rollups — the bytes they previously
  // tracked are no longer reachable. Per domain-model.md §StorageVolume
  // a missing volume is operationally equivalent to "absent for the
  // capacity calculation"; surface them in the per-row table below
  // (so operators can see the regression) but exclude from the totals.
  const allItems: StorageVolume[] = volumes.data?.items ?? [];
  const items: StorageVolume[] = allItems.filter((v) => v.status !== 'missing');
  const totalCap = items.reduce((s, v) => s + v.capacity_bytes, 0);
  const totalUsed = items.reduce((s, v) => s + v.used_bytes, 0);
  // RETENTION stat surfaces the longest retention_duration across
  // currently-enabled RecordingPolicies — the practical "how far back
  // can I scrub" answer for the operator. Polling at 30s matches the
  // Policies page cadence so the stat reacts quickly when an MS-pushed
  // or local policy mutation lands. Disabled policies are excluded;
  // the recorder doesn't apply them today, so their retention is
  // irrelevant for this rollup. Empty list → '—'.
  const policies = usePoll(fetchRecordingPolicies, 30_000, []);
  const longestRetentionNs = (policies.data?.items ?? [])
    .filter((p) => p.enabled)
    .reduce((max, p) => (p.retention_duration > max ? p.retention_duration : max), 0);
  const usedPct = totalCap > 0 ? (totalUsed / totalCap) * 100 : 0;
  const free = totalCap - totalUsed;
  const totalWriteBps = items.reduce(
    (s, v) => s + (v.write_bytes_per_second ?? 0),
    0,
  );
  // Days-at-current-rate estimate: free bytes / current aggregate
  // write rate. Useful as a coarse "you've got ~N days before things
  // overflow" signal. Falls back to "—" while priming or when free
  // can't be computed.
  const daysRemaining =
    totalWriteBps > 0 && free > 0
      ? Math.floor(free / totalWriteBps / 86400)
      : null;
  return (
    <div
      style={{
        flex: 1,
        display: 'flex',
        flexDirection: 'column',
        minWidth: 0,
        background: 'var(--bg-primary)',
        overflow: 'auto',
      }}
    >
      <PageHeader
        breadcrumb="RECORDING SERVER / STORAGE"
        title="Storage"
        sub={
          volumes.status === 'error'
            ? `Recorder unreachable — ${volumes.error.message}`
            : volumes.status === 'loading'
              ? 'Loading volumes…'
              : `${items.length} volume${items.length === 1 ? '' : 's'} · ${formatBytes(totalCap)} total`
        }
      />
      <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: 12 }}>
          <Stat
            label="USED"
            value={formatBytes(totalUsed)}
            unit=""
            tone="accent"
            sub={`${usedPct.toFixed(1)}% of ${formatBytes(totalCap)}`}
            icon="pie-chart"
          />
          <Stat
            label="FREE"
            value={formatBytes(free)}
            unit=""
            tone="success"
            sub={daysRemaining !== null ? `≈ ${daysRemaining} DAYS AT CURRENT RATE` : 'ROLLING RETENTION'}
            icon="database"
          />
          <Stat
            label="WRITE RATE"
            value={
              totalWriteBps > 0 ? `${((totalWriteBps * 8) / 1e6).toFixed(1)}` : totalWriteBps === 0 && volumes.data ? '0.0' : '—'
            }
            unit="Mb/s"
            tone="accent"
            sub="AGGREGATE ACROSS VOLUMES"
            icon="arrow-down"
          />
          {/* RETENTION: max retention_duration across enabled
              RecordingPolicies. Surfaced from /v1/recording-policies. */}
          <Stat
            label="RETENTION"
            value={longestRetentionNs > 0 ? formatDuration(longestRetentionNs) : '—'}
            unit=""
            tone="accent"
            sub={
              policies.status === 'error'
                ? 'POLICIES UNREACHABLE'
                : longestRetentionNs > 0
                  ? 'LONGEST ACTIVE POLICY'
                  : policies.status === 'loading'
                    ? 'LOADING POLICIES…'
                    : 'NO ACTIVE POLICY'
            }
            icon="rotate-ccw"
          />
        </div>
        <Card>
          <SectionHeader>STORAGE BREAKDOWN</SectionHeader>
          {/* Used-vs-free breakdown bar — high-level state of capacity. */}
          <div
            style={{
              height: 16,
              display: 'flex',
              borderRadius: 4,
              overflow: 'hidden',
              border: '1px solid var(--border)',
              marginTop: 8,
            }}
          >
            <div style={{ flex: totalUsed || 1, background: '#F97316' }} />
            <div style={{ flex: free || 1, background: 'var(--bg-tertiary)' }} />
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2,1fr)', gap: 10, marginTop: 12 }}>
            <LegendItem color="#F97316" label="Used" value={formatBytes(totalUsed)} />
            <LegendItem color="var(--text-muted)" label="Free" value={formatBytes(free)} />
          </div>
        </Card>
        {/* Wave 5: Content Type Breakdown. Live /v1/storage-volumes/breakdown
            rollup by content_type (continuous / motion / scheduled /
            event-triggered / off), plus optional per-camera drilldown. */}
        <Card>
          <SectionHeader>CONTENT TYPE BREAKDOWN</SectionHeader>
          {breakdown.status === 'error' && (
            <div
              style={{
                padding: 16,
                fontFamily: 'var(--font-mono)',
                fontSize: 11,
                color: 'var(--text-muted)',
                letterSpacing: 0.5,
              }}
            >
              BREAKDOWN UNREACHABLE — {breakdown.error.message}
            </div>
          )}
          {breakdown.status !== 'error' && (
            <ContentTypeBreakdownBody
              data={breakdown.data}
              cameras={cameras.data?.items ?? []}
              drilldownOpen={drilldownOpen}
              onToggleDrilldown={() => setDrilldownOpen((v) => !v)}
            />
          )}
        </Card>
        <Card style={{ padding: 0 }}>
          <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)' }}>
            <SectionHeader style={{ margin: 0 }}>VOLUMES · {allItems.length}</SectionHeader>
          </div>
          {allItems.length === 0 && (
            <div
              style={{
                padding: 30,
                textAlign: 'center',
                fontFamily: 'var(--font-mono)',
                fontSize: 11,
                color: 'var(--text-muted)',
                letterSpacing: 1,
              }}
            >
              {volumes.status === 'loading' ? 'LOADING…' : 'NO VOLUMES MOUNTED'}
            </div>
          )}
          {allItems.map((v, i) => {
            // SMART block is optional: present when smartctl
            // resolved + parsed, absent on hosts without
            // smartmontools or when the mount-path doesn't resolve
            // to a /dev/* device. Compose the secondary row from
            // whatever we got; fall back to "checked at" timestamp.
            const smart = v.smart;
            const driveLine = smart
              ? [
                  smart.model_family || smart.model_name,
                  smart.power_on_hours !== undefined
                    ? `${smart.power_on_hours.toLocaleString()}H`
                    : null,
                  smart.health_passed === true
                    ? 'SMART PASSED'
                    : smart.health_passed === false
                      ? 'SMART FAILED'
                      : null,
                  smart.temperature_c !== undefined ? `${smart.temperature_c}°C` : null,
                ]
                  .filter(Boolean)
                  .join(' · ')
              : `CHECKED ${new Date(v.last_checked_at).toLocaleTimeString('en-GB', { hour12: false })}`;
            const writeRate = v.write_bytes_per_second;
            return (
              <div
                key={v.id}
                style={{
                  padding: 14,
                  borderTop: i === 0 ? 'none' : '1px solid var(--border)',
                  display: 'grid',
                  gridTemplateColumns: '160px 1fr 200px 100px',
                  gap: 14,
                  alignItems: 'center',
                }}
              >
                <span
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 13,
                    color: '#F97316',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {v.mount_path}
                </span>
                <div>
                  <div style={{ fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-primary)' }}>
                    {v.kind} · priority {v.priority}
                    {writeRate !== undefined && writeRate > 0 && (
                      <span style={{ color: 'var(--text-muted)', fontFamily: 'var(--font-mono)', marginLeft: 10 }}>
                        · {((writeRate * 8) / 1e6).toFixed(1)} Mb/s write
                      </span>
                    )}
                  </div>
                  <div
                    style={{
                      fontFamily: 'var(--font-mono)',
                      fontSize: 10,
                      color: 'var(--text-muted)',
                      marginTop: 3,
                      letterSpacing: 0.5,
                      textTransform: 'uppercase',
                    }}
                  >
                    {driveLine}
                  </div>
                </div>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                  <Progress value={v.capacity_bytes > 0 ? (v.used_bytes / v.capacity_bytes) * 100 : 0} />
                  <span
                    style={{
                      fontFamily: 'var(--font-mono)',
                      fontSize: 10,
                      color: 'var(--text-muted)',
                      letterSpacing: 0.5,
                    }}
                  >
                    {formatBytes(v.used_bytes)} / {formatBytes(v.capacity_bytes)}
                  </span>
                </div>
                <StatusBadge kind={statusBadge(v.status)} />
              </div>
            );
          })}
        </Card>
      </div>
    </div>
  );
}

function LegendItem({ color, label, value }: { color: string; label: string; value: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
      <span style={{ width: 10, height: 10, borderRadius: 2, background: color }} />
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
          {label}
        </div>
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-primary)', marginTop: 2 }}>
          {value}
        </div>
      </div>
    </div>
  );
}

interface ContentTypeBreakdownBodyProps {
  data: import('../lib/api').StorageBreakdownResponse | undefined;
  cameras: import('../lib/api').Camera[];
  drilldownOpen: boolean;
  onToggleDrilldown: () => void;
}

// ContentTypeBreakdownBody renders the stacked-bar visual + detail
// table for /v1/storage-volumes/breakdown. Per Wave 5 we ship the
// minimum: a horizontal stacked bar by content_type, a row per
// content_type with size / segments / share-of-total, and a
// collapsed-by-default per-camera drilldown.
function ContentTypeBreakdownBody({
  data,
  cameras,
  drilldownOpen,
  onToggleDrilldown,
}: ContentTypeBreakdownBodyProps) {
  if (!data || data.segment_count === 0) {
    return (
      <div
        style={{
          padding: 14,
          fontFamily: 'var(--font-mono)',
          fontSize: 11,
          color: 'var(--text-muted)',
          letterSpacing: 0.5,
        }}
      >
        NO RECORDED FOOTAGE YET — content type breakdown will populate as
        segments seal.
      </div>
    );
  }

  const total = data.total_bytes;
  // Stable order: content_type alphabetically, matching the recorder's
  // wire order. Keeps colors stable across renders.
  const entries = data.by_content_type;

  return (
    <>
      {/* Stacked bar — width proportional to byte_size per content_type. */}
      <div
        style={{
          height: 16,
          display: 'flex',
          borderRadius: 4,
          overflow: 'hidden',
          border: '1px solid var(--border)',
          marginTop: 8,
        }}
      >
        {entries.map((e) => (
          <div
            key={e.content_type}
            title={`${CONTENT_TYPE_LABELS[e.content_type] ?? e.content_type}: ${formatBytes(e.byte_size)}`}
            style={{
              flex: e.byte_size || 1,
              background: CONTENT_TYPE_COLORS[e.content_type] ?? '#94A3B8',
            }}
          />
        ))}
      </div>
      {/* Per-content-type rows with size + segments + share-of-total. */}
      <div
        style={{
          marginTop: 12,
          display: 'grid',
          gridTemplateColumns: '20px 1fr 110px 110px 80px',
          gap: 8,
          fontSize: 11,
          fontFamily: 'var(--font-mono)',
        }}
      >
        <div />
        <div style={{ color: 'var(--text-muted)', letterSpacing: 1 }}>CONTENT TYPE</div>
        <div style={{ color: 'var(--text-muted)', letterSpacing: 1, textAlign: 'right' }}>SIZE</div>
        <div style={{ color: 'var(--text-muted)', letterSpacing: 1, textAlign: 'right' }}>SEGMENTS</div>
        <div style={{ color: 'var(--text-muted)', letterSpacing: 1, textAlign: 'right' }}>SHARE</div>
        {entries.map((e) => {
          const pct = total > 0 ? (e.byte_size / total) * 100 : 0;
          return (
            <ContentTypeRow
              key={e.content_type}
              entry={e}
              pct={pct}
            />
          );
        })}
      </div>
      <div
        style={{
          marginTop: 16,
          paddingTop: 12,
          borderTop: '1px solid var(--border)',
        }}
      >
        <button
          type="button"
          onClick={onToggleDrilldown}
          style={{
            background: 'transparent',
            border: 'none',
            cursor: 'pointer',
            padding: 0,
            fontFamily: 'var(--font-mono)',
            fontSize: 11,
            color: 'var(--text-primary)',
            letterSpacing: 1,
            textTransform: 'uppercase',
          }}
        >
          {drilldownOpen ? 'HIDE' : 'SHOW'} PER-CAMERA DRILLDOWN ({data.by_camera.length})
        </button>
        {drilldownOpen && (
          <PerCameraDrilldown by_camera={data.by_camera} cameras={cameras} />
        )}
      </div>
    </>
  );
}

function ContentTypeRow({
  entry,
  pct,
}: {
  entry: StorageBreakdownEntry;
  pct: number;
}) {
  const color = CONTENT_TYPE_COLORS[entry.content_type] ?? '#94A3B8';
  const label = CONTENT_TYPE_LABELS[entry.content_type] ?? entry.content_type;
  return (
    <>
      <span
        style={{
          width: 10,
          height: 10,
          borderRadius: 2,
          background: color,
          alignSelf: 'center',
        }}
      />
      <div style={{ color: 'var(--text-primary)' }}>{label}</div>
      <div style={{ color: 'var(--text-primary)', textAlign: 'right' }}>
        {formatBytes(entry.byte_size)}
      </div>
      <div style={{ color: 'var(--text-muted)', textAlign: 'right' }}>
        {entry.segment_count.toLocaleString()}
      </div>
      <div style={{ color: 'var(--text-muted)', textAlign: 'right' }}>
        {pct.toFixed(1)}%
      </div>
    </>
  );
}

function PerCameraDrilldown({
  by_camera,
  cameras,
}: {
  by_camera: StorageBreakdownByCamera[];
  cameras: import('../lib/api').Camera[];
}) {
  // Resolve camera_id → name for the drilldown header. Drop unmatched
  // entries gracefully — the recorder may have orphan footage from a
  // camera that's been removed but whose segments haven't been pruned.
  const nameByID = new Map<string, string>();
  for (const c of cameras) nameByID.set(c.id, c.name);

  if (by_camera.length === 0) {
    return (
      <div
        style={{
          marginTop: 10,
          padding: 14,
          fontFamily: 'var(--font-mono)',
          fontSize: 11,
          color: 'var(--text-muted)',
          letterSpacing: 0.5,
        }}
      >
        NO PER-CAMERA FOOTAGE.
      </div>
    );
  }

  return (
    <div style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 10 }}>
      {by_camera.map((cam) => {
        const camName = nameByID.get(cam.camera_id) ?? cam.camera_id;
        return (
          <div
            key={cam.camera_id}
            style={{
              padding: 12,
              border: '1px solid var(--border)',
              borderRadius: 4,
              background: 'var(--bg-secondary)',
            }}
          >
            <div
              style={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
                marginBottom: 8,
              }}
            >
              <div
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 12,
                  color: '#F97316',
                }}
              >
                {camName}
              </div>
              <div
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 11,
                  color: 'var(--text-muted)',
                  letterSpacing: 0.5,
                }}
              >
                {formatBytes(cam.byte_size)} · {cam.segment_count.toLocaleString()} SEG
              </div>
            </div>
            <div
              style={{
                height: 8,
                display: 'flex',
                borderRadius: 2,
                overflow: 'hidden',
                border: '1px solid var(--border)',
              }}
            >
              {cam.by_content_type.map((e) => (
                <div
                  key={e.content_type}
                  title={`${CONTENT_TYPE_LABELS[e.content_type] ?? e.content_type}: ${formatBytes(e.byte_size)}`}
                  style={{
                    flex: e.byte_size || 1,
                    background: CONTENT_TYPE_COLORS[e.content_type] ?? '#94A3B8',
                  }}
                />
              ))}
            </div>
            <div
              style={{
                marginTop: 6,
                display: 'flex',
                flexWrap: 'wrap',
                gap: 10,
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                color: 'var(--text-muted)',
                letterSpacing: 0.5,
              }}
            >
              {cam.by_content_type.map((e) => (
                <span
                  key={e.content_type}
                  style={{ display: 'flex', alignItems: 'center', gap: 4 }}
                >
                  <span
                    style={{
                      width: 8,
                      height: 8,
                      borderRadius: 2,
                      background: CONTENT_TYPE_COLORS[e.content_type] ?? '#94A3B8',
                    }}
                  />
                  {(CONTENT_TYPE_LABELS[e.content_type] ?? e.content_type).toUpperCase()}{' '}
                  {formatBytes(e.byte_size)} · {e.segment_count.toLocaleString()}
                </span>
              ))}
            </div>
          </div>
        );
      })}
    </div>
  );
}
