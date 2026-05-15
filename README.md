# raft-inspector

> [!NOTE]
> This codebase is LLM-generated.

Offline inspection tool for OpenBao/Vault raft storage. Reads `raft.db` and `vault.db` directly without requiring a running server. Works while the server is running (copies files to bypass locks).

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
raft-inspector -d <data-dir> status
raft-inspector -d <data-dir> log [index] [-n <count>] [--stats] \
    [--decrypt --unseal-key-file <path>]
raft-inspector -d <data-dir> fsm [--top] [--prefix <prefix>] \
    [--decrypt --unseal-key-file <path>] [--limit <n>]
raft-inspector -d <data-dir> snapshot <file> [--keys] \
    [--decrypt --unseal-key-file <path>] [--limit <n>]
```

### Global flags

| Flag | Description |
|------|-------------|
| `-d`, `--data-dir` | Path to the OpenBao/Vault data directory (required). Expects `<data-dir>/raft/raft.db` and `<data-dir>/vault.db`. |
| `--max-value-length` | Max bytes of decrypted value to display (default 256, 0=unlimited). |

### Commands

**status** — Combined health overview reading both `raft/raft.db` and `vault.db`.

**log** — List or inspect raft log entries with decoded operations.

| Flag | Description |
|------|-------------|
| `[index]` | Show a single log entry by index (positional argument). |
| `-n`, `--count` | Show last N entries. |
| `--stats` | Show log statistics: operation distribution and hot keys. |
| `--decrypt` | Decrypt values (requires `--unseal-key-file`). |
| `--unseal-key-file` | Path to the init JSON file produced by `bao operator init`. |

**fsm** — Inspect the FSM state (`vault.db` data bucket).

| Flag | Description |
|------|-------------|
| `--top` | Show top-level key path segments with counts. |
| `--prefix` | List keys matching a prefix. |
| `--decrypt` | Decrypt values (requires `--unseal-key-file`). |
| `--unseal-key-file` | Path to the init JSON file produced by `bao operator init`. |
| `--limit` | Max number of keys to display (0=unlimited). |

**snapshot** — Inspect an external snapshot archive.

| Flag | Description |
|------|-------------|
| `<file>` | Path to the snapshot file (positional argument, required). |
| `--keys` | List all key paths in the snapshot. |
| `--decrypt` | Decrypt values (requires `--unseal-key-file`). |
| `--unseal-key-file` | Path to the init JSON file produced by `bao operator init`. |
| `--limit` | Max number of keys to display (0=unlimited). |

## Decryption

Decryption currently supports Shamir seal with threshold=1 only. The init JSON file is expected to have this structure:

```json
{
  "unseal_keys_b64": ["..."],
  "unseal_threshold": 1
}
```

Key derivation chain:
1. Unseal key (from init JSON, base64-decoded)
2. Decrypt `core/hsm/barrier-unseal-keys` → root key
3. Root key decrypts `core/keyring` → per-term AES-256 encryption keys
4. Term keys decrypt individual entries

## Field descriptions

### status

| Field | Source | Description |
|-------|--------|-------------|
| Current Term | raft/raft.db | Raft election epoch; increments each time a new leader election occurs. |
| First Log Index | raft/raft.db | Oldest log entry still retained in the log store. |
| Last Log Index | raft/raft.db | Most recent log entry written to the log store. |
| Entry Count | raft/raft.db | Number of log entries currently retained (last - first + 1). |
| Last Vote Cand | raft/raft.db | Node this server last voted for in a leader election. |
| Last Vote Term | raft/raft.db | Term in which the last vote was cast. |
| Applied Index | vault.db | Last log entry applied to the FSM (state machine). |
| Applied Term | vault.db | Term of the last applied log entry. |
| Config Index | vault.db | Log index at which the current cluster membership was committed. |
| Servers | vault.db | Cluster members: voter=participates in elections/quorum, nonvoter=replica only. |
| Desired Suffrage | vault.db | Role this node wants to have in the cluster (voter or nonvoter). |
| Unapplied Entries | computed | Log entries not yet applied to the FSM; should be 0 on a healthy node. |
| Trailing Entries | computed | Applied entries kept in the log for follower catch-up without full snapshot. |
| Snapshot Index | computed | Highest index that was truncated; entries at or below this were compacted away. |
| File Size | os.Stat | Total size of the BoltDB file on disk. |
| DB Logical Size | bolt.Tx.Size | Pages allocated by BoltDB (file may be larger due to OS allocation). |
| Page Size | bolt.DB.Info | BoltDB page size; all allocations are in multiples of this. |
| Free Pages | bolt.DB.Stats | Pages released by deletes but not yet returned to OS; reused for future writes. |
| Pending Pages | bolt.DB.Stats | Pages freed in current transaction, not yet available for reuse. |
| Freelist In-Use | bolt.DB.Stats | Bytes used by BoltDB's internal freelist tracking structure. |

### log

| Field | Source | Description |
|-------|--------|-------------|
| Index | raft/raft.db | Sequence number of this entry in the raft log; monotonically increasing. |
| Term | raft/raft.db | Election term when this entry was created by the leader. |
| Type | raft/raft.db | Entry type: LogCommand (data op), LogConfiguration (membership change), LogBarrier, LogNoop. |
| AppendedAt | raft/raft.db | Wall-clock time when the leader appended this entry to its log. |
| Operations | raft/raft.db | Decoded operations: op type (put/delete/beginTx/commitTx/verifyRead/verifyList), storage key path, and value size. |

### log --stats

| Field | Source | Description |
|-------|--------|-------------|
| Time Range | raft/raft.db | Wall-clock range from oldest to newest log entry's AppendedAt timestamp. |
| Entry Count | raft/raft.db | Total number of log entries in the retained log. |
| Total/Avg/Max Size | raft/raft.db | Byte sizes of log entry Data payloads (encrypted operations). |
| Op Distribution | raft/raft.db | Count of each operation type (put, delete, beginTx, commitTx, verifyRead, verifyList) across all log entries. |
| Hot Keys | raft/raft.db | Storage paths most frequently written to; helps identify write-heavy workloads. |

### fsm

| Field | Source | Description |
|-------|--------|-------------|
| Keys | vault.db | Plaintext storage paths from the vault.db data bucket; values are AES-GCM encrypted. |
| Top-level segments | vault.db | Correspond to subsystems (core/, sys/, logical/) and their key counts. |

### snapshot

| Field | Source | Description |
|-------|--------|-------------|
| Index | meta.json | Raft log index at which this snapshot was taken. |
| Term | meta.json | Raft term at the time of snapshot. |
| Servers | meta.json | Cluster membership recorded in the snapshot (voter/nonvoter). |
| Checksums | SHA256SUMS | SHA-256 integrity verification of archive contents. |
| Total Keys | state.bin | Number of key/value entries in the FSM state dump. |
| Total Size | state.bin | Sum of all value bytes (encrypted); does not include key path sizes. |
