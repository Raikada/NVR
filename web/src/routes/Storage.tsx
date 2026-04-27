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
import { fetchStorageVolumes } from '../lib/api';
import type { StorageVolume } from '../lib/api';
import { usePoll, formatBytes } from '../lib/hooks';

function statusBadge(s: StorageVolume['status']): StatusBadgeKind {
  if (s === 'online') return 'online';
  if (s === 'degraded') return 'degraded';
  if (s === 'full' || s === 'read_only') return 'error';
  return 'offline';
}

export function Storage() {
  const volumes = usePoll(fetchStorageVolumes, 30_000, []);
  const items: StorageVolume[] = volumes.data?.items ?? [];
  const totalCap = items.reduce((s, v) => s + v.capacity_bytes, 0);
  const totalUsed = items.reduce((s, v) => s + v.used_bytes, 0);
  const usedPct = totalCap > 0 ? (totalUsed / totalCap) * 100 : 0;
  const free = totalCap - totalUsed;
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
            // STUB: "≈ N days at current rate" needs a write-rate
            // observer the recorder doesn't expose yet.
            sub="ROLLING RETENTION"
            icon="database"
          />
          {/* WRITE RATE: not exposed by /v1/. STUB. */}
          <Stat
            label="WRITE RATE"
            value="—"
            unit=""
            tone="accent"
            sub="NOT EXPOSED"
            icon="arrow-down"
          />
          {/* RETENTION: surfaced via RecordingPolicy.RetentionDuration
              once /v1/recording-policies wires up. STUB. */}
          <Stat
            label="RETENTION"
            value="—"
            unit=""
            tone="accent"
            sub="POLICY-DRIVEN"
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
            <SectionHeader style={{ margin: 0 }}>VOLUMES · {items.length}</SectionHeader>
          </div>
          {items.length === 0 && (
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
          {items.map((v, i) => (
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
                </div>
                <div
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 10,
                    color: 'var(--text-muted)',
                    marginTop: 3,
                    letterSpacing: 0.5,
                  }}
                >
                  {/* SMART hours: STUB. Drive vendor-model: STUB. */}
                  CHECKED {new Date(v.last_checked_at).toLocaleTimeString('en-GB', { hour12: false })}
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
          ))}
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
