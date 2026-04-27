// Package api: disk-backed durable audit buffer per ADR 0006 D6.
//
// The in-memory ring (audit_buffer.go) is the v1 surface; this is the
// durable promotion called for by ADR 0006 D6 ("when the Management
// Server is unreachable, the recorder buffers audit entries durably to
// local disk and replays them in order on reconnect"). Audit entries
// must survive recorder restart; the chain head must be restored so
// the cryptographic chain continues across the boundary; corruption
// must be detected loudly rather than silently papered over.
//
// Format. Append-only length-prefixed log file:
//
//	<u32-be length><canonical-JSON AuditLogEntry bytes>
//
// One record per AuditLogEntry. The canonical-JSON serializer is the
// same one used for chain hashing (defs.CanonicalJSONAuditLogEntry),
// so a re-serialized recovered entry hashes identically and chain
// verification on recovery is exact.
//
// Sync. Every Append calls Sync() before returning. Audit entries are
// security-relevant; we trade write throughput for durability. The
// alternative — buffered writes flushed on a timer — opens a window
// where a power loss drops entries the API has already acknowledged,
// which is incompatible with the integrity property. Per-entry fsync
// is the right tradeoff here. Performance cost is real (Linux ext4
// fsync round-trip on the order of 1-10ms per call); the recorder's
// audit volume is bounded ("low-GB per 30 days" per ADR 0006 D6) and
// dominated by configuration applies, authentication events, and
// occasional admin actions — not a high-frequency path.
//
// Recovery. On Open, the disk log is walked end-to-end:
//   - Each entry's EntryHash is recomputed and compared to its stored
//     value. A mismatch is a corruption — Open returns an error and
//     the caller is expected to halt audit emission.
//   - Each entry's PrevHash is compared to the prior entry's
//     EntryHash. The first entry must have PrevHash = ZeroPrevHash.
//     A mismatch is a chain-break — Open returns an error.
//   - On success the most-recent N entries (capacity 4096) are loaded
//     into the in-memory ring; the chain head is set to the last
//     entry's EntryHash. AuditChain reads HeadHashOnDisk() to restore
//     its prevHash on construction.
//
// Retention / compaction. Compaction drops entries with RecordedAt
// older than the retention window (default 30 days, tunable via
// Conf.AuditRetentionDays). The chain remains intact post-compaction:
// dropped entries are evicted from disk and from the in-memory ring,
// but their cryptographic hashes were already incorporated into the
// chain head and the post-compaction first entry's PrevHash field is
// untouched (it still references the dropped entry's EntryHash). A
// downstream verifier that ingested the chain pre-compaction has the
// gap-bridging hash; one starting fresh post-compaction sees an
// orphaned PrevHash on the first surviving entry and is expected to
// treat it as a "chain pre-existed before this point" signal rather
// than a chain break. (ADR 0006 D8 immutability-during-retention is
// preserved: dropped entries are dropped because they are past the
// retention horizon.)
//
// Degraded threshold. Per ADR 0006 D6, the buffer enters degraded
// mode at 80% of capacity. For the disk-backed buffer, capacity is
// AuditMaxDiskBytes (default 100 MB, tunable). When the on-disk log
// crosses 80% of that ceiling, IsDegraded() returns true and admin-
// gating (audit_gate.go) refuses new admin actions. Recording is
// never gated.
package api //nolint:revive

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// jsonUnmarshalAuditEntry decodes a canonical-JSON-serialized
// AuditLogEntry payload back into a struct. Canonical-JSON is a
// strict subset of standard JSON, so encoding/json suffices for the
// reverse direction.
func jsonUnmarshalAuditEntry(payload []byte, e *defs.AuditLogEntry) error {
	return json.Unmarshal(payload, e)
}

// auditDiskRecordHeaderSize is the size of the u32-big-endian length
// prefix.
const auditDiskRecordHeaderSize = 4

// auditDiskMaxRecordSize is a sanity cap on a single record's payload.
// AuditLogEntry serialized to canonical-JSON is well under 64 KiB even
// with maxed attributes; a length prefix that exceeds this is treated
// as corruption.
const auditDiskMaxRecordSize = 1 << 20 // 1 MiB

// AuditDiskBuffer is the disk-backed durable audit buffer.
//
// Concurrency: all exported methods are safe for concurrent callers.
// Append serializes through mu; Snapshot/Len/IsDegraded use the same
// lock under read access.
type AuditDiskBuffer struct {
	mu sync.RWMutex

	path           string
	maxDiskBytes   int64
	retentionWindow time.Duration

	file     *os.File
	diskSize int64

	// in-memory hot ring; capacity matches the in-memory buffer's
	// default (4096) so reads are served from RAM at the same shape
	// as the in-memory implementation.
	capacity int
	items    []defs.AuditLogEntry

	// headHash is the EntryHash of the last entry written to disk, or
	// defs.ZeroPrevHash if the log is empty after recovery. The chain
	// reads this on construction to continue the sequence across
	// restarts.
	headHash string
}

// AuditDiskBufferOptions bundles construction parameters for
// AuditDiskBuffer.Open. Zero-value fields fall back to defaults.
type AuditDiskBufferOptions struct {
	// Dir is the directory the audit-chain log lives in. The log
	// file is created at <Dir>/audit-chain.log. The directory is
	// created (mkdir -p) if absent.
	Dir string

	// MaxDiskBytes is the on-disk size ceiling that drives the
	// degraded threshold. Default 100 MiB.
	MaxDiskBytes int64

	// RetentionDays is the retention window for compaction. Entries
	// with RecordedAt older than (now - RetentionDays) are dropped on
	// the next Compact call. Default 30 days.
	RetentionDays int

	// Capacity is the in-memory hot ring's capacity. Default 4096
	// (matches the in-memory AuditBuffer).
	Capacity int
}

// auditDiskLogFilename is the on-disk log file's basename within the
// configured directory.
const auditDiskLogFilename = "audit-chain.log"

// defaultAuditMaxDiskBytes is the on-disk size ceiling default per
// the implementation note in ADR 0006 D6 ("low-GB" was the rough
// envelope; 100 MiB is a conservative starting point). Tunable via
// Conf.AuditMaxDiskBytes.
const defaultAuditMaxDiskBytes int64 = 100 * 1024 * 1024

// defaultAuditRetentionDays is the retention-compaction default per
// ADR 0006 D8 ("retention principles only; specific Ns deferred").
// 30 days is the operational floor that matches the buffer-size
// envelope in D6 ("30 days of typical audit volume").
const defaultAuditRetentionDays = 30

// ErrAuditChainCorrupt signals an integrity failure detected during
// disk-log recovery — a hash mismatch or a broken prev/entry link.
// Per ADR 0006 D3 a chain mismatch is "a critical alert; it is not
// silently dropped"; the recorder should halt audit emission until
// operator intervention rather than continue with a tainted chain.
var ErrAuditChainCorrupt = errors.New("audit chain corruption detected on disk; halt audit emission")

// OpenAuditDiskBuffer opens or creates the durable audit buffer at
// the configured directory and rebuilds the in-memory ring + chain
// head from the on-disk log.
//
// Returns ErrAuditChainCorrupt (wrapped) when integrity verification
// fails. Callers MUST treat this as a hard failure: silent recovery
// from a tainted chain defeats the audit integrity property.
func OpenAuditDiskBuffer(opts AuditDiskBufferOptions) (*AuditDiskBuffer, error) {
	if opts.Dir == "" {
		return nil, fmt.Errorf("AuditDiskBuffer: Dir is required")
	}
	if opts.MaxDiskBytes <= 0 {
		opts.MaxDiskBytes = defaultAuditMaxDiskBytes
	}
	if opts.RetentionDays <= 0 {
		opts.RetentionDays = defaultAuditRetentionDays
	}
	if opts.Capacity <= 0 {
		opts.Capacity = defaultAuditBufferCapacity
	}

	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("AuditDiskBuffer: mkdir %q: %w", opts.Dir, err)
	}

	logPath := filepath.Join(opts.Dir, auditDiskLogFilename)
	f, err := os.OpenFile(logPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("AuditDiskBuffer: open %q: %w", logPath, err)
	}

	b := &AuditDiskBuffer{
		path:           logPath,
		maxDiskBytes:   opts.MaxDiskBytes,
		retentionWindow: time.Duration(opts.RetentionDays) * 24 * time.Hour,
		file:           f,
		capacity:       opts.Capacity,
		items:          make([]defs.AuditLogEntry, 0, opts.Capacity),
		headHash:       defs.ZeroPrevHash,
	}

	if err := b.recover(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return b, nil
}

// recover walks the on-disk log end-to-end, verifies chain
// integrity, and populates the in-memory ring + headHash. Returns
// ErrAuditChainCorrupt-wrapped on any integrity failure.
//
// recover holds no lock — it is called from OpenAuditDiskBuffer
// before the buffer is published to other goroutines.
func (b *AuditDiskBuffer) recover() error {
	if _, err := b.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("AuditDiskBuffer: seek-start: %w", err)
	}

	r := bufio.NewReader(b.file)
	var (
		prevEntryHash = defs.ZeroPrevHash
		offset        int64
		count         int
	)

	for {
		var lenBuf [auditDiskRecordHeaderSize]byte
		_, err := io.ReadFull(r, lenBuf[:])
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			// Trailing torn write: truncate at offset and continue.
			// This is the one recovery path that is *not* a chain
			// integrity failure: a power loss between length-prefix
			// write and payload write leaves a partial tail. Truncate
			// the partial bytes and proceed; nothing was acknowledged
			// to a caller because Append fsyncs after the full record.
			if err := b.file.Truncate(offset); err != nil {
				return fmt.Errorf("AuditDiskBuffer: truncate torn-tail: %w", err)
			}
			break
		}
		if err != nil {
			return fmt.Errorf("AuditDiskBuffer: read length: %w", err)
		}

		recLen := binary.BigEndian.Uint32(lenBuf[:])
		if recLen == 0 || recLen > auditDiskMaxRecordSize {
			return fmt.Errorf("%w: implausible record length %d at offset %d",
				ErrAuditChainCorrupt, recLen, offset)
		}

		payload := make([]byte, recLen)
		_, err = io.ReadFull(r, payload)
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			// Torn tail: a length prefix landed but the payload did
			// not (or only partially did). Truncate at offset and
			// proceed; nothing was acknowledged because Append's
			// fsync follows the full record.
			if err := b.file.Truncate(offset); err != nil {
				return fmt.Errorf("AuditDiskBuffer: truncate torn-tail: %w", err)
			}
			break
		}
		if err != nil {
			return fmt.Errorf("AuditDiskBuffer: read payload: %w", err)
		}

		entry, err := decodeAuditDiskRecord(payload)
		if err != nil {
			return fmt.Errorf("%w: decode at offset %d: %v",
				ErrAuditChainCorrupt, offset, err)
		}

		recomputed, err := defs.ComputeAuditEntryHash(entry)
		if err != nil {
			return fmt.Errorf("%w: hash recompute at offset %d: %v",
				ErrAuditChainCorrupt, offset, err)
		}
		if recomputed != entry.EntryHash {
			return fmt.Errorf("%w: entry_hash mismatch at offset %d (entry id=%s)",
				ErrAuditChainCorrupt, offset, entry.ID)
		}
		if entry.PrevHash != prevEntryHash {
			return fmt.Errorf("%w: prev_hash break at offset %d (entry id=%s, expected prev=%s, got=%s)",
				ErrAuditChainCorrupt, offset, entry.ID, prevEntryHash, entry.PrevHash)
		}

		// Append into the hot ring (oldest-first eviction).
		if len(b.items) >= b.capacity {
			copy(b.items, b.items[1:])
			b.items = b.items[:len(b.items)-1]
		}
		b.items = append(b.items, entry)

		prevEntryHash = entry.EntryHash
		offset += int64(auditDiskRecordHeaderSize) + int64(recLen)
		count++
	}

	b.diskSize = offset
	b.headHash = prevEntryHash

	// Position the file for subsequent appends.
	if _, err := b.file.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("AuditDiskBuffer: seek-end after recover: %w", err)
	}
	return nil
}

// decodeAuditDiskRecord deserializes a single payload back into an
// AuditLogEntry. The on-disk encoding is canonical-JSON; canonical-
// JSON is a strict subset of standard JSON so encoding/json suffices
// for decode.
func decodeAuditDiskRecord(payload []byte) (defs.AuditLogEntry, error) {
	var e defs.AuditLogEntry
	if err := jsonUnmarshalAuditEntry(payload, &e); err != nil {
		return defs.AuditLogEntry{}, err
	}
	return e, nil
}

// Append durably persists the entry to disk (with fsync) and adds it
// to the in-memory ring. The entry must already be chain-stamped:
// EntryHash, PrevHash, and the rest of the canonical fields are set
// by the AuditChain producer. Append does not re-hash or re-link.
//
// Append is the security-critical write path; per ADR 0006 D6 every
// Append fsyncs to disk before returning. Callers tolerate the
// per-entry fsync cost in exchange for durability.
func (b *AuditDiskBuffer) Append(entry defs.AuditLogEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()

	canon, err := defs.CanonicalJSONAuditLogEntry(entry)
	if err != nil {
		// Canonicalization failure is a hard internal bug. We can't
		// silently drop the entry — that breaks the integrity
		// property — and we can't return an error from this signature
		// without breaking the in-memory buffer's contract. Log to
		// stderr and panic; the caller's emit path is the right place
		// to convert this into a clean shutdown signal.
		panic(fmt.Sprintf("AuditDiskBuffer: canonical-json failure: %v", err))
	}

	var lenBuf [auditDiskRecordHeaderSize]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(canon)))

	// Single Write call where possible. We use two writes to avoid
	// allocating a combined buffer; both writes go to the same FD and
	// are followed by a single Sync. A torn write between the two
	// writes is recovered by recover()'s torn-tail truncation.
	if _, err := b.file.Write(lenBuf[:]); err != nil {
		panic(fmt.Sprintf("AuditDiskBuffer: write length: %v", err))
	}
	if _, err := b.file.Write(canon); err != nil {
		panic(fmt.Sprintf("AuditDiskBuffer: write payload: %v", err))
	}
	if err := b.file.Sync(); err != nil {
		panic(fmt.Sprintf("AuditDiskBuffer: fsync: %v", err))
	}

	b.diskSize += int64(auditDiskRecordHeaderSize) + int64(len(canon))

	if len(b.items) >= b.capacity {
		copy(b.items, b.items[1:])
		b.items = b.items[:len(b.items)-1]
	}
	b.items = append(b.items, entry)
	b.headHash = entry.EntryHash
}

// Snapshot returns a copy of the in-memory ring's contents in
// oldest-first order. Matches AuditBuffer.Snapshot.
func (b *AuditDiskBuffer) Snapshot() []defs.AuditLogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]defs.AuditLogEntry, len(b.items))
	copy(out, b.items)
	return out
}

// GetByID returns the entry with the given id from the in-memory ring,
// or false if absent. Older-than-ring entries that survive on disk
// are not searched here; range queries belong on a future on-disk
// index, not the hot path.
func (b *AuditDiskBuffer) GetByID(id string) (defs.AuditLogEntry, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for i := range b.items {
		if b.items[i].ID == id {
			return b.items[i], true
		}
	}
	return defs.AuditLogEntry{}, false
}

// Len returns the in-memory ring's current size.
func (b *AuditDiskBuffer) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.items)
}

// Capacity returns the in-memory ring's capacity. The disk store has
// its own capacity (AuditMaxDiskBytes) which IsDegraded reads.
func (b *AuditDiskBuffer) Capacity() int {
	return b.capacity
}

// IsDegraded reports whether the on-disk log has crossed the 80%
// high-water threshold per ADR 0006 D6. When true, the admin-action
// gate (audit_gate.go) refuses new admin actions.
func (b *AuditDiskBuffer) IsDegraded() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	// diskSize >= maxDiskBytes * 4/5 ; integer arithmetic to avoid
	// float drift, matching the in-memory buffer's form.
	return b.diskSize*int64(auditDegradedThresholdDenominator) >=
		b.maxDiskBytes*int64(auditDegradedThresholdNumerator)
}

// Clear empties the in-memory ring AND truncates the disk log. Used
// by tests; production callers do not Clear (the chain machinery
// preserves history per ADR 0006 D8).
func (b *AuditDiskBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.items = b.items[:0]
	if err := b.file.Truncate(0); err != nil {
		panic(fmt.Sprintf("AuditDiskBuffer: truncate on Clear: %v", err))
	}
	if _, err := b.file.Seek(0, io.SeekStart); err != nil {
		panic(fmt.Sprintf("AuditDiskBuffer: seek on Clear: %v", err))
	}
	b.diskSize = 0
	b.headHash = defs.ZeroPrevHash
}

// HeadHashOnDisk returns the EntryHash of the last record on disk,
// or ZeroPrevHash when the log is empty. AuditChain reads this on
// construction to restore its prevHash across restarts.
func (b *AuditDiskBuffer) HeadHashOnDisk() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.headHash
}

// DiskSize returns the on-disk log's current byte size. Tests use
// this to validate compaction and degraded-threshold behavior.
func (b *AuditDiskBuffer) DiskSize() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.diskSize
}

// Compact drops on-disk records whose RecordedAt is older than the
// retention window. The post-compaction first record's PrevHash
// remains as written (referencing the dropped predecessor's
// EntryHash); the chain head is unchanged. The in-memory ring is
// rebuilt from the post-compaction tail.
//
// Implementation: walk the existing log, write the surviving records
// to a temp file in the same directory, rename atomically over the
// live log. The buffer file handle is swapped to the new file under
// the write lock.
//
// Compact is safe to call concurrently with Append; serialization is
// through the buffer's mutex, so an Append in flight either lands
// before the compaction snapshot point (and is preserved) or after
// the rename (and writes to the new file). It is NOT safe to call
// Compact concurrently with another Compact; callers should drive
// compaction from a single goroutine.
func (b *AuditDiskBuffer) Compact(now time.Time) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	cutoff := now.Add(-b.retentionWindow)

	// Re-walk the existing file, collecting surviving records.
	if _, err := b.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("AuditDiskBuffer.Compact: seek-start: %w", err)
	}
	r := bufio.NewReader(b.file)

	tmpPath := b.path + ".compact.tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("AuditDiskBuffer.Compact: open tmp: %w", err)
	}
	cleanupTmp := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	var (
		newSize  int64
		newItems = make([]defs.AuditLogEntry, 0, b.capacity)
		newHead  = defs.ZeroPrevHash
	)

	for {
		var lenBuf [auditDiskRecordHeaderSize]byte
		_, readErr := io.ReadFull(r, lenBuf[:])
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			cleanupTmp()
			return fmt.Errorf("AuditDiskBuffer.Compact: read length: %w", readErr)
		}
		recLen := binary.BigEndian.Uint32(lenBuf[:])
		payload := make([]byte, recLen)
		if _, readErr := io.ReadFull(r, payload); readErr != nil {
			cleanupTmp()
			return fmt.Errorf("AuditDiskBuffer.Compact: read payload: %w", readErr)
		}
		entry, decErr := decodeAuditDiskRecord(payload)
		if decErr != nil {
			cleanupTmp()
			return fmt.Errorf("AuditDiskBuffer.Compact: decode: %w", decErr)
		}

		if entry.RecordedAt.Before(cutoff) {
			// Drop. Chain link is preserved through the next surviving
			// entry's PrevHash field, which still references the
			// dropped predecessor's EntryHash.
			continue
		}

		// Survivor — write to tmp.
		if _, err := tmp.Write(lenBuf[:]); err != nil {
			cleanupTmp()
			return fmt.Errorf("AuditDiskBuffer.Compact: write length: %w", err)
		}
		if _, err := tmp.Write(payload); err != nil {
			cleanupTmp()
			return fmt.Errorf("AuditDiskBuffer.Compact: write payload: %w", err)
		}
		newSize += int64(auditDiskRecordHeaderSize) + int64(recLen)

		if len(newItems) >= b.capacity {
			copy(newItems, newItems[1:])
			newItems = newItems[:len(newItems)-1]
		}
		newItems = append(newItems, entry)
		newHead = entry.EntryHash
	}

	if err := tmp.Sync(); err != nil {
		cleanupTmp()
		return fmt.Errorf("AuditDiskBuffer.Compact: fsync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("AuditDiskBuffer.Compact: close tmp: %w", err)
	}

	// Atomic rename over the live log. Close the live FD first so
	// platforms that don't allow rename-over-open-file (Windows)
	// behave consistently; on POSIX this is also fine.
	if err := b.file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("AuditDiskBuffer.Compact: close live: %w", err)
	}
	if err := os.Rename(tmpPath, b.path); err != nil {
		// Try to recover the live handle.
		f, openErr := os.OpenFile(b.path, os.O_RDWR|os.O_CREATE, 0o600)
		if openErr == nil {
			b.file = f
		}
		return fmt.Errorf("AuditDiskBuffer.Compact: rename: %w", err)
	}

	f, err := os.OpenFile(b.path, os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("AuditDiskBuffer.Compact: reopen: %w", err)
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		_ = f.Close()
		return fmt.Errorf("AuditDiskBuffer.Compact: seek-end: %w", err)
	}

	b.file = f
	b.diskSize = newSize
	b.items = newItems
	if newHead != defs.ZeroPrevHash {
		// Chain head is the latest surviving entry's hash; if the log
		// is now empty (everything past retention), b.headHash stays
		// at its prior value — see the note below.
		b.headHash = newHead
	}
	// If everything was compacted out, b.headHash retains the last
	// pre-compaction value: the chain head must not regress to
	// ZeroPrevHash, because that would let a subsequent Append open a
	// new chain origin and orphan the dropped tail. The chain
	// continues across compaction; an empty disk log is not the same
	// as a virgin chain.
	return nil
}

// Close releases the underlying file handle. Open buffers should be
// Close'd at recorder shutdown.
func (b *AuditDiskBuffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.file == nil {
		return nil
	}
	err := b.file.Close()
	b.file = nil
	return err
}
