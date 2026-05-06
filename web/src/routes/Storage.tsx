// Storage route — disk overview, breakdown bar, RAID disk list.
// Wired to /v1/storage-volumes for capacity / used / status / mount
// path. Drive vendor-model strings, SMART hours, and the per-
// content-type breakdown (continuous / motion / AI) stay STUB —
// the recorder doesn't expose drive vendor metadata or per-content
// breakdown today; both would need a recorder-side API extension.

import {
  Card,
  Progress,
  SectionHeader,
  Stat,
  StatusBadge,
} from '../components/primitives';
import type { StatusBadgeKind } from '../components/primitives';
import { PageHeader } from '../components/PageHeader';
import { fetchRecordingPolicies, fetchStorageVolumes } from '../lib/api';
import type { StorageVolume } from '../lib/api';
import { usePoll, formatBytes } from '../lib/hooks';
import { formatDuration } from '../lib/duration';

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
          {/* STUB: per-content-type breakdown (continuous / motion /
              AI) requires recorder-side accounting that doesn't
              exist yet. Two-segment used-vs-free is the live data
              we have today. */}
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
