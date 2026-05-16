# raft-inspector

> [!NOTE]
> This codebase is LLM-generated.

Developer tool for offline inspection of OpenBao/Vault raft storage. Reads `raft.db` and `vault.db` directly without requiring a running server. Works while the server is running (copies files to bypass locks).

See [raft-inspector.md](raft-inspector.md) for a full walkthrough with example output against a 3-node cluster.

## Build

```bash
go build -o raft-inspector .
```

or install

```bash
go install github.com/tsaarni/raft-inspector@latest
```

## Usage

```
raft-inspector status <data-dir>
raft-inspector log <data-dir> [range] [--stats] \
    [--unseal-key-file <path>]
raft-inspector fsm <data-dir> [--prefix <prefix>] \
    [--unseal-key-file <path>] [--limit <n>]
raft-inspector snapshot <file> [--prefix <prefix>] \
    [--unseal-key-file <path>] [--limit <n>]
```

### Global flags

| Flag | Description |
|------|-------------|
| `--max-value-length` | Max bytes of decrypted value to display (default 256, 0=unlimited). |

### Commands

**status** — Combined health overview reading both `raft/raft.db` and `vault.db`.

| Flag | Description |
|------|-------------|
| `<data-dir>` | Path to the OpenBao/Vault data directory (positional, required). |

**log** — List or inspect raft log entries with decoded operations.

| Flag | Description |
|------|-------------|
| `<data-dir>` | Path to the OpenBao/Vault data directory (positional, required). |
| `[range]` | Index or range: `5` (single entry), `1..10` (index 1 to 10), `~10` (last 10 entries). Without this, all entries are shown. |
| `--stats` | Show log statistics: operation distribution and hot keys. |
| `--unseal-key-file` | Path to the init JSON file produced by `bao operator init`. Enables decryption. |

**fsm** — Inspect the FSM state (`vault.db` data bucket). Shows total key count, top-level path segments, and largest keys by default.

| Flag | Description |
|------|-------------|
| `<data-dir>` | Path to the OpenBao/Vault data directory (positional, required). |
| `--prefix` | List keys matching a prefix (shows encrypted size per key). |
| `--unseal-key-file` | Path to the init JSON file produced by `bao operator init`. Enables decryption. |
| `--limit` | Max number of keys to display (0=unlimited). |

**snapshot** — Inspect an external snapshot archive. Shows metadata, checksum verification, top-level path segments, and largest keys by default.

| Flag | Description |
|------|-------------|
| `<file>` | Path to the snapshot file (positional argument, required). |
| `--prefix` | List keys matching a prefix (shows encrypted size per key). |
| `--unseal-key-file` | Path to the init JSON file produced by `bao operator init`. Enables decryption. |
| `--limit` | Max number of keys to display (0=unlimited). |

## How the database files work

Integrated storage uses two database files per node:

**`raft/raft.db`** holds the replicated write-ahead log. Every write operation (secret put, lease creation, policy change) is first appended here as a log entry, then replicated to other nodes. OpenBao/Vault automatically takes periodic snapshots of the current state and truncates old log entries, keeping only a trailing window (default ~10000 entries) for follower catch-up. The file grows with write activity but freed space from truncated entries is reused internally. The on-disk file size stays roughly stable after initial growth.

**`vault.db`** holds the current state: all secrets, configuration, leases, and internal metadata. Log entries from `raft.db` are applied here as key/value writes and deletes. This is the "result" of replaying the log. Its size reflects the actual volume of stored data. When secrets or leases are deleted, the freed space is reused for future writes but the file does not shrink.

**Manual snapshot restore** (`bao/vault operator raft snapshot restore`) replaces `vault.db` with a fresh file built from the snapshot contents. The `raft.db` is not modified — existing log entries remain and are reused for follower catch-up. The new `vault.db` contains only live data with no accumulated free pages, which is why snapshot restore is the primary way to reclaim disk space for `vault.db`.

**Index and term** are the two values that identify any log entry. The **index** is a cluster-wide sequence number that increases by one for each write. It never resets and all nodes agree on what each index contains. The **term** is an epoch counter that increments each time a new leader is elected. It is used internally for leader election and consistency checks between nodes but does not affect log replay. Both are stored in `raft/raft.db` as part of each log entry. The `vault.db` config bucket records the last applied index so that after a restart, the node knows where to resume applying log entries.

**Buckets** are BoltDB's equivalent of tables — each database file contains a few named buckets that organize data by purpose.

`raft/raft.db` has two buckets:
- **`logs`** — the raft log entries, keyed by index. Each entry is a protobuf-encoded message containing one or more operations (put, delete, etc.).
- **`conf`** — election state: current term, last vote candidate, and last vote term. Persisted so the node can safely resume after a restart without violating election rules.

`vault.db` has two buckets:
- **`data`** — all application state (secrets, leases, policies, mounts, auth config, and internal cryptographic material like the keyring and unseal keys under `core/`).
- **`config`** — FSM metadata: last applied log index/term, cluster membership, and this node's desired role (voter/nonvoter).


## Decryption

The tool supports decryption with Shamir seal (threshold=1 only). Passing `--unseal-key-file` enables decryption. The init JSON file must match the format produced by `bao operator init -format=json`:

```json
{
  "unseal_keys_b64": ["..."],
  "unseal_threshold": 1
}
```

## Field descriptions

### status

| Field | Source | Description |
|-------|--------|-------------|
| Current Term | raft/raft.db | Raft election epoch; increments on each leader election. Rapidly increasing = network instability. |
| First Log Index | raft/raft.db | Oldest log entry retained. Advances as snapshots compact old entries away. |
| Last Log Index | raft/raft.db | Most recent log entry written. Continuously increasing on an active cluster. |
| Entry Count | raft/raft.db | Retained log entries (last − first + 1). Typically near `trailing_logs` config (default 10000). |
| Last Vote Cand | raft/raft.db | Node this server last voted for in a leader election. |
| Last Vote Term | raft/raft.db | Term in which the last vote was cast. Should be close to Current Term. |
| Applied Index | vault.db | Last log entry applied to the FSM. Should equal or be very close to Last Log Index. |
| Applied Term | vault.db | Term of the last applied log entry. |
| Config Index | vault.db | Log index at which current cluster membership was committed. Changes on add/remove. |
| Servers | vault.db | Cluster members: voter = participates in quorum, nonvoter = read replica only. |
| Desired Suffrage | vault.db | Role this node wants (voter/nonvoter). Mismatch with actual = promotion/demotion pending. |
| Unapplied Entries | computed | Last Log Index − Applied Index. Should be 0 on a healthy idle node; large gap = FSM falling behind. |
| Trailing Entries | computed | Applied entries kept in log for follower catch-up without full snapshot transfer. |
| Snapshot Index | computed | First Log Index − 1. Highest index compacted into a snapshot; never advancing = snapshots broken. |
| File Size | os.Stat | Total on-disk size of the database file. |
| DB Logical Size | bolt.Tx.Size | Portion of the file actively used by the database. Difference from File Size is preallocation. |
| Page Size | bolt.DB.Info | Internal allocation unit (typically 4096 bytes). |
| Free Pages | bolt.DB.Stats | Unused space from past deletions, reused for future writes but not returned to OS. High % = snapshot restore would shrink the file. |
| Pending Pages | bolt.DB.Stats | Pages being freed; will become reusable after the next write. Normally 0 on an idle node. |
| Freelist In-Use | bolt.DB.Stats | Overhead for tracking free pages internally. |
| Space Efficiency | computed | Percentage of on-disk file size occupied by live data (excludes free pages and preallocation). For `vault.db`, the live data size is the estimated file size after snapshot restore. For `raft.db`, snapshot restore has no effect. |
| Bucket (per-bucket) | bolt.Bucket.Stats | Key count, tree depth, branch/leaf page utilization %. Branch pages hold internal routing keys; leaf pages hold actual data. Utilization = in-use / allocated bytes. 50–100% is normal. Lower values occur after heavy deletions (e.g., lease revocations). Space is reused on new writes; snapshot restore reclaims disk. |
| Integrity Check | bolt.Tx.Check | Consistency check across all database pages. OK = healthy. FAILED = data corruption, investigate immediately. |

### log

| Field | Source | Description |
|-------|--------|-------------|
| Index | raft/raft.db | Monotonically increasing sequence number identifying this entry in the raft log. |
| Term | raft/raft.db | Election term when the leader created this entry. |
| Type | raft/raft.db | `LogCommand` = data op, `LogConfiguration` = membership change, `LogBarrier` = consistency fence, `LogNoop` = leader establishment after election. |
| AppendedAt | raft/raft.db | Wall-clock time when the leader appended this entry. Shows `(+offset)` relative to first displayed entry. |
| Operations | raft/raft.db | Decoded ops: put (write key), delete (remove key), beginTx/commitTx (transaction boundaries), verifyRead/verifyList (optimistic concurrency checks), restoreCallback (post-snapshot-restore signal). |

### log --stats

| Field | Source | Description |
|-------|--------|-------------|
| Time Range | raft/raft.db | Wall-clock span from oldest to newest retained log entry. |
| Entry Count | raft/raft.db | Total retained log entries. |
| Total/Avg/Max Size | raft/raft.db | Byte sizes of log entry payloads (encrypted). Large max = bulk writes or big secrets. |
| Op Distribution | raft/raft.db | Count per operation type. Helps identify workload pattern (write-heavy, transactional, etc.). |
| Hot Keys | raft/raft.db | Top storage paths by write frequency. Normal hot keys: `core/lock`, `sys/expire/`, `core/leader`. Unexpected ones may indicate misbehaving plugins or lease storms. |

### fsm

| Field | Source | Description |
|-------|--------|-------------|
| Keys | vault.db | Plaintext storage paths in the data bucket. Values are AES-GCM encrypted with the keyring. |
| Top-level segments | vault.db | First path segment groups: `core/` = internal state, `sys/` = system backend (policies, leases, mounts), `logical/` = secrets engines, `auth/` = auth methods. Key count per segment shows relative data volume. |
| Largest keys | vault.db | Top 10 entries by encrypted value size. |

### snapshot

| Field | Source | Description |
|-------|--------|-------------|
| Index | meta.json | Raft log index captured in this snapshot. All state up to this index is included. |
| Term | meta.json | Raft term at snapshot time. |
| Servers | meta.json | Cluster membership at snapshot time. May be stale if snapshot is old. |
| Checksums | SHA256SUMS | SHA-256 integrity verification. ✓ = intact, ✗ = corruption during transfer or storage. |
| Total Keys | state.bin | Number of key/value entries in the full FSM state dump. Useful for comparing snapshots over time. |
| Top-level segments | state.bin | First path segment groups and their key counts (same as fsm command). |
| Largest keys | state.bin | Top 10 entries by encrypted value size. |
