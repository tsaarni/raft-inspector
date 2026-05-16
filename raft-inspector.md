# raft-inspector demo

This document demonstrates all `raft-inspector` commands against a 3-node OpenBao raft cluster. All commands shown below can be reproduced by running them in order.

## Cluster setup

Start a 3-node OpenBao cluster using Docker containers with host networking. The raft data directories are volume-mounted so that `raft-inspector` can access them from the host.

### Create data directories and configs

```console
$ mkdir -p testdata/node0 testdata/node1 testdata/node2
```

```console
$ cat <<'EOF' > testdata/node0.hcl
listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = true
}

storage "raft" {
  path    = "/host/node0"
  node_id = "node0"
}

api_addr      = "http://127.0.0.1:8200"
cluster_addr  = "http://127.0.0.1:8201"
EOF
$ cat <<'EOF' > testdata/node1.hcl
listener "tcp" {
  address     = "0.0.0.0:8202"
  tls_disable = true
}

storage "raft" {
  path    = "/host/node1"
  node_id = "node1"
}

api_addr      = "http://127.0.0.1:8202"
cluster_addr  = "http://127.0.0.1:8203"
EOF
$ cat <<'EOF' > testdata/node2.hcl
listener "tcp" {
  address     = "0.0.0.0:8204"
  tls_disable = true
}

storage "raft" {
  path    = "/host/node2"
  node_id = "node2"
}

api_addr      = "http://127.0.0.1:8204"
cluster_addr  = "http://127.0.0.1:8205"
EOF
```

### Start the nodes

Start all three nodes as background containers. The `--user` flag ensures files are owned by the current user. The `testdata` directory is mounted as `/host` inside the container.

```console
$ docker run -d --name bao-node0 \
    --network host \
    --user $(id -u):$(id -g) \
    -v $PWD/testdata:/host \
    ghcr.io/openbao/openbao:2.5.3 server -config=/host/node0.hcl > /dev/null
$ docker run -d --name bao-node1 \
    --network host \
    --user $(id -u):$(id -g) \
    -v $PWD/testdata:/host \
    ghcr.io/openbao/openbao:2.5.3 server -config=/host/node1.hcl > /dev/null
$ docker run -d --name bao-node2 \
    --network host \
    --user $(id -u):$(id -g) \
    -v $PWD/testdata:/host \
    ghcr.io/openbao/openbao:2.5.3 server -config=/host/node2.hcl > /dev/null
```

Wait for node0 to be ready.

### Initialize and unseal

Initialize the cluster on node0 with a single unseal key (for simplicity).

```console
$ docker exec -e BAO_ADDR=http://127.0.0.1:8200 bao-node0 \
    bao operator init -key-shares=1 -key-threshold=1 -format=json \
    > testdata/init.json
```

Unseal node0 — it becomes the raft leader.

```console
$ docker exec -e BAO_ADDR=http://127.0.0.1:8200 bao-node0 \
    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json) > /dev/null
```

Join node1 and node2 to the cluster, then unseal them.

```console
$ docker exec -e BAO_ADDR=http://127.0.0.1:8202 bao-node1 \
    bao operator raft join http://127.0.0.1:8200 > /dev/null
$ docker exec -e BAO_ADDR=http://127.0.0.1:8204 bao-node2 \
    bao operator raft join http://127.0.0.1:8200 > /dev/null
```

Unseal node1 and node2.

```console
$ docker exec -e BAO_ADDR=http://127.0.0.1:8202 bao-node1 \
    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json) > /dev/null
$ docker exec -e BAO_ADDR=http://127.0.0.1:8204 bao-node2 \
    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json) > /dev/null
```

Verify the cluster peers.

```console
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao operator raft list-peers
Node     Address           State       Voter
----     -------           -----       -----
node0    127.0.0.1:8201    leader      true
node1    127.0.0.1:8203    follower    false
node2    127.0.0.1:8205    follower    false
```

## Create test data

Enable a PKI secrets engine and generate a self-signed root CA.

```console
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao secrets enable pki
Success! Enabled the pki secrets engine at: pki/
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao secrets tune -max-lease-ttl=87600h pki
Success! Tuned the secrets engine at: pki/
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao write -field=certificate pki/root/generate/internal \
    common_name='Test Root CA' ttl=87600h > /dev/null
```

Enable a KV v2 secrets engine and write some secrets. Then update and delete entries to generate varied raft log operations.

```console
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao secrets enable -path=secret kv-v2
Success! Enabled the kv-v2 secrets engine at: secret/
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao kv put secret/myapp/config \
    endpoint=https://api.example.com api_key=secret > /dev/null
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao kv put secret/myapp/config \
    endpoint=https://api.example.com api_key=updated > /dev/null
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao kv put secret/myapp/credentials \
    username=admin password=mypassword > /dev/null
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao kv delete secret/myapp/credentials
Success! Data deleted (if it existed) at: secret/data/myapp/credentials
```

Write secrets in bulk, then disable the engine to delete all data at once. This simulates churn and produces free pages visible in the status output.

```console
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao secrets enable -path=tmp kv-v2
Success! Enabled the kv-v2 secrets engine at: tmp/
$ for i in $(seq 1 5); do docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao kv put tmp/$i value=$(head -c 16384 /dev/urandom | base64 -w0); done > /dev/null
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao secrets disable tmp
Success! Disabled the secrets engine (if it existed) at: tmp/
```

## raft-inspector status

Combined health overview reading both `raft/raft.db` and `vault.db`. Note the Space Efficiency metric showing how much of the file is live data, and the estimated size after snapshot restore.

```console
$ ./raft-inspector -d testdata/node0 status
─── raft/raft.db stable store ───
  Current Term:       3
  First Log Index:    1
  Last Log Index:     75
  Entry Count:        75
  Last Vote Cand:     127.0.0.1:8201
  Last Vote Term:     3

─── vault.db config bucket ───
  Applied Index:      75  (config/latest_indexes)
  Applied Term:       3  (config/latest_indexes)
  Config Index:       31  (config/latest_config)
  Servers:              (config/latest_config)
    - node0 (127.0.0.1:8201) voter
    - node1 (127.0.0.1:8203) nonvoter
    - node2 (127.0.0.1:8205) nonvoter
  Desired Suffrage:   voter  (config/local_node_config)

─── Computed ───
  Unapplied Entries:  0
  Trailing Entries:   74
  Snapshot Index:     0

─── BoltDB Stats: raft/raft.db ───
  File Size:          17 MB (16801792 bytes)
  DB Logical Size:    377 kB (376832 bytes)
  Page Size:          4.1 kB
  Free Pages:         35 (143 kB, 0.9%)
  Pending Pages:      0
  Freelist In-Use:    0 B
  Space Efficiency:   1.4% (234 kB live data)
  Bucket "conf":      3 keys, depth 1, branch 0% leaf 0% utilization
  Bucket "logs":      75 keys, depth 2, branch 13% leaf 73% utilization
  Total:              78 keys, branch 13% leaf 73% utilization
  Integrity Check:    OK

─── BoltDB Stats: vault.db ───
  File Size:          17 MB (16801792 bytes)
  DB Logical Size:    340 kB (339968 bytes)
  Page Size:          4.1 kB
  Free Pages:         69 (283 kB, 1.7%)
  Pending Pages:      0
  Freelist In-Use:    0 B
  Space Efficiency:   0.3% (57 kB live data)
  Bucket "config":    3 keys, depth 1, branch 0% leaf 0% utilization
  Bucket "data":      47 keys, depth 2, branch 18% leaf 60% utilization
  Total:              50 keys, branch 18% leaf 60% utilization
  Integrity Check:    OK

  Current Term       Raft election epoch; increments each time a new leader election occurs. [raft/raft.db]
  First Log Index    Oldest log entry still retained in the log store. [raft/raft.db]
  Last Log Index     Most recent log entry written to the log store. [raft/raft.db]
  Entry Count        Number of log entries currently retained (last - first + 1). [raft/raft.db]
  Last Vote Cand     Node this server last voted for in a leader election. [raft/raft.db]
  Last Vote Term     Term in which the last vote was cast. [raft/raft.db]
  Applied Index      Last log entry applied to the FSM (state machine). [vault.db]
  Applied Term       Term of the last applied log entry. [vault.db]
  Config Index       Log index at which the current cluster membership was committed. [vault.db]
  Servers            Cluster members: voter=participates in elections/quorum, nonvoter=replica only. [vault.db]
  Desired Suffrage   Role this node wants to have in the cluster (voter or nonvoter). [vault.db]
  Unapplied Entries  Log entries not yet applied to the FSM; should be 0 on a healthy node. [computed]
  Trailing Entries   Applied entries kept in the log for follower catch-up without full snapshot. [computed]
  Snapshot Index     Highest index that was truncated; entries at or below this were compacted away. [computed]
  File Size          Total size of the BoltDB file on disk. [os.Stat]
  DB Logical Size    Pages allocated by BoltDB (file may be larger due to preallocation). [bolt.Tx.Size]
  Page Size          BoltDB page size; all allocations are in multiples of this. [bolt.DB.Info]
  Free Pages         Pages released by deletes but not yet returned to OS; reused for future writes. [bolt.DB.Stats]
  Pending Pages      Pages freed in current transaction, not yet available for reuse. [bolt.DB.Stats]
  Freelist In-Use    Bytes used by BoltDB's internal freelist tracking structure. [bolt.DB.Stats]
  Space Efficiency   Percentage of file occupied by live data (excludes free pages and preallocation). [computed]
  Bucket <name>      Per-bucket B+tree: key count, depth, branch/leaf page utilization %. [bolt.Bucket.Stats]
  Integrity Check    Verifies all pages are reachable or freed, no double refs. [bolt.Tx.Check]
```

Take a snapshot and restore it to reclaim space. After restore, `vault.db` is rebuilt from scratch — its file size should match the estimate above. The `raft.db` retains all log entries; they are only truncated by automatic snapshot compaction (once entry count exceeds `snapshot_threshold`).

```console
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao operator raft snapshot save /host/backup.snap
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao operator raft snapshot restore -force /host/backup.snap
```

```console
$ docker exec -e BAO_ADDR=http://127.0.0.1:8200 bao-node0 \
    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json) > /dev/null
```

```console
$ ./raft-inspector -d testdata/node0 status 2>&1 \
    | grep -E '(─── BoltDB|File Size:|DB Logical Size:|Free Pages:|Space Efficiency:)'
─── BoltDB Stats: raft/raft.db ───
  File Size:          17 MB (16801792 bytes)
  DB Logical Size:    377 kB (376832 bytes)
  Free Pages:         34 (139 kB, 0.8%)
  Space Efficiency:   1.4% (238 kB live data)
─── BoltDB Stats: vault.db ───
  File Size:          131 kB (131072 bytes)
  DB Logical Size:    78 kB (77824 bytes)
  Free Pages:         3 (12 kB, 9.4%)
  Space Efficiency:   50.0% (66 kB live data)
```

## raft-inspector log

Show log entries with decrypted values. The `put` operations reveal the actual stored data.

```console
$ ./raft-inspector -d testdata/node0 log -n 3 \
    --unseal-key-file testdata/init.json
─── raft/raft.db logs bucket (entries 1 to 80, showing 78 to 80) ───

─── Index 78 (raft/raft.db logs/78) ───
  Index:      78
  Term:       3
  Type:       LogCommand
  AppendedAt: 2026-05-16 09:49:11.994513334 +0000 UTC  (+0s)
  Operations:
    [op=4/restoreCallback]   (0 B)

─── Index 79 (raft/raft.db logs/79) ───
  Index:      79
  Term:       3
  Type:       LogCommand
  AppendedAt: 2026-05-16 09:49:17.010978165 +0000 UTC  (+5.016s)
  Operations:
    [op=2/put] core/lock  (36 B)
      b566a37f-48cb-3b33-23da-86450d0d559e

─── Index 80 (raft/raft.db logs/80) ───
  Index:      80
  Term:       3
  Type:       LogCommand
  AppendedAt: 2026-05-16 09:49:17.038437342 +0000 UTC  (+5.044s)
  Operations:
    [op=2/put] core/leader/b566a37f-48cb-3b33-23da-86450d0d559e  (1.5 kB)
      {"redirect_addr":"http://127.0.0.1:8200","cluster_addr":"https://127.0.0.1:8201","cluster_cert":"MIICejCCAdygAwIBAgIISqSbTLFI4DEwCgYIKoZIzj0EAwQwMjEwMC4GA1UEAxMnZnctZGJiYTA5NjctNTAwZi1kNzdkLWFkNDEtMzkxZWExN2JmMGU2MCAXDTI2MDUxNjA5NDgzNVoYDzIwNTYwNTE1MjE0OTA [...truncated, 1.5 kB total]


  Index        Sequence number of this entry in the raft log; monotonically increasing. [raft/raft.db]
  Term         Election term when this entry was created by the leader. [raft/raft.db]
  Type         Entry type: LogCommand (data op), LogConfiguration (membership change), LogBarrier, LogNoop. [raft/raft.db]
  AppendedAt   Wall-clock time when the leader appended this entry to its log. [raft/raft.db]
  Operations   Decoded operations: op type (put/delete), storage key path, and encrypted value size. [raft/raft.db]
```

## raft-inspector log --stats

Analyze log entry patterns: operation distribution and hot keys.

```console
$ ./raft-inspector -d testdata/node0 log --stats
─── Log Statistics ───
  Time Range:         0001-01-01 00:00:00 +0000 UTC → 2026-05-16 09:49:17.038437342 +0000 UTC
  Entry Count:        80
  Total Size:         154 kB
  Average Size:       1.9 kB
  Max Size:           23 kB

─── Operation Distribution ───
  put                 72
  verifyRead          46
  delete              27
  commitTx            18
  beginTx             18
  verifyList          7
  restoreCallback     1

─── Hot Keys (top 10) ───
                                                              37
  core/mounts                                                 14
  core/mounts/0786ad9c-b5c2-48ac-c495-600932fd7448            6
  logical/1b5aa0b3-2d58-b1d4-9dc9-f0d3b7efd8f3/crls/config    4
  logical/99c94a79-1bfb-920e-0393-bea341cc4b3b/b5e7a2fa-37ff-de14-7d3c-21c86a161649/metadata/5kKG4HR271ZcJ5qvbhkLCqxqFS35sTP0M1CDUFccg3EZGGsoYqcHKwcMaO9Yka/p0DYyFXhRzuM9N9NvNrcs6O9NyZgf6tn7jy7wG48CMVZDMb5qGKqaxVO3igiIFVcSGF4
  core/mounts/1b5aa0b3-2d58-b1d4-9dc9-f0d3b7efd8f3            4
  logical/99c94a79-1bfb-920e-0393-bea341cc4b3b/b5e7a2fa-37ff-de14-7d3c-21c86a161649/metadata/5kKG4HR271ZcJ5qvbhkLCqxqFS35sTP0M1CDUFccg3EZGGsoYqcHKwcMaO9Yka/1TfChKBxqahbTJqMMs1lmmjbthEwbhQOpRYP1ZQXfNJMtESNC2It0zvzSvQ2QBdeg6nmm2r704
  logical/0786ad9c-b5c2-48ac-c495-600932fd7448/c0f81da9-db8c-4122-d4ee-a7571a797c79/metadata/18y9GJnWYWa6JWJ8tCq1FW9AewDVP9qdBJD7M0UB13ScF1zRfOTKDbE293
  logical/0786ad9c-b5c2-48ac-c495-600932fd7448/c0f81da9-db8c-4122-d4ee-a7571a797c79/upgrading3
  logical/0786ad9c-b5c2-48ac-c495-600932fd7448/c0f81da9-db8c-4122-d4ee-a7571a797c79/versions/27b/f047ad492e0934ab1a3483142f7eca57cf1f2bc42c69af5849c85e2994a493

  Time Range         Wall-clock range from oldest to newest log entry's AppendedAt timestamp. [raft/raft.db]
  Entry Count        Total number of log entries in the retained log. [raft/raft.db]
  Total/Avg/Max Size Byte sizes of log entry Data payloads (encrypted operations). [raft/raft.db]
  Op Distribution    Count of each operation type (put, delete, etc.) across all log entries. [raft/raft.db]
  Hot Keys           Storage paths most frequently written to; helps identify write-heavy workloads. [raft/raft.db]
```

## raft-inspector fsm

Show total key count, top-level key path segments, and largest keys in the FSM data store (`vault.db`).

```console
$ ./raft-inspector -d testdata/node0 fsm
─── State Data ───
  Total Keys:     47

─── Top-level Key Segments ───
  core            21
  logical         21
  sys             5

─── Largest Keys ───
     2.8 kB  logical/1b5aa0b3-2d58-b1d4-9dc9-f0d3b7efd8f3/config/issuer/9d93432a-4d92-aae3-729a-e450c43af295
     2.7 kB  sys/policy/default
     1.8 kB  logical/1b5aa0b3-2d58-b1d4-9dc9-f0d3b7efd8f3/config/key/639999ab-686e-fcfc-cee1-6a6b65a0ddaf
     1.7 kB  core/raft/tls
     1.5 kB  core/leader/b566a37f-48cb-3b33-23da-86450d0d559e
      908 B  logical/99c94a79-1bfb-920e-0393-bea341cc4b3b/b5e7a2fa-37ff-de14-7d3c-21c86a161649/policy/metadata
      836 B  logical/1b5aa0b3-2d58-b1d4-9dc9-f0d3b7efd8f3/certs/2f-ae-62-59-76-a8-e0-4e-df-2f-39-bd-27-a4-cc-b8-04-e0-42-18
      574 B  logical/99c94a79-1bfb-920e-0393-bea341cc4b3b/b5e7a2fa-37ff-de14-7d3c-21c86a161649/archive/metadata
      535 B  core/wrapping/jwtkey
      528 B  sys/token/id/h7c6870f38117fbc2f7dd45c1fb05b25acfee8b0d9e39c3e0830bb711f34908b2

  Keys are plaintext storage paths from the vault.db data bucket; values are AES-GCM encrypted. [vault.db]
  Top-level segments correspond to subsystems (core/, sys/, logical/) and their key counts. [vault.db]
  Largest keys shows top 10 entries by encrypted value size. [vault.db]
```

## raft-inspector fsm --prefix

List FSM keys matching a prefix. Shows encrypted value size after each key.

```console
$ ./raft-inspector -d testdata/node0 fsm --prefix sys/policy/
─── Keys matching prefix: sys/policy/ ───
sys/policy/default  (2.7 kB)
sys/policy/response-wrapping  (347 B)

  Keys are plaintext storage paths from the vault.db data bucket; values are AES-GCM encrypted. [vault.db]
  Size shown after each key is the encrypted (ciphertext) size, not the plaintext size. [vault.db]
```

Show decrypted values for keys matching a prefix.

```console
$ ./raft-inspector -d testdata/node0 fsm --prefix sys/policy/ \
    --unseal-key-file testdata/init.json
─── Keys matching prefix: sys/policy/ ───
sys/policy/default  (2.7 kB)
  {"Version":2,"DataVersion":1,"CASRequired":false,"Raw":"\n# Allow tokens to look up their own properties\npath \"auth/token/lookup-self\" {\n    capabilities = [\"read\"]\n}\n\n# Allow tokens to renew themselves\npath \"auth/token/renew-self\" {\n    capab [...truncated, 2.7 kB total]
sys/policy/response-wrapping  (347 B)
  {"Version":2,"DataVersion":1,"CASRequired":false,"Raw":"\npath \"cubbyhole/response\" {\n    capabilities = [\"create\", \"read\"]\n}\n\npath \"sys/wrapping/unwrap\" {\n    capabilities = [\"update\"]\n}\n","Templated":false,"Type":0,"Expiration":"0001-01- [...truncated, 314 B total]

  Keys are plaintext storage paths from the vault.db data bucket; values are AES-GCM encrypted. [vault.db]
  Size shown after each key is the encrypted (ciphertext) size, not the plaintext size. [vault.db]
  --unseal-key-file decrypts values using the keyring derived from the unseal key. [vault.db]
```

## raft-inspector snapshot

Inspect the snapshot archive. Default mode shows metadata, checksum verification, top-level key segments, and largest keys — same as `fsm`.

```console
$ ./raft-inspector snapshot testdata/backup.snap
─── Snapshot Metadata ───
  Index:          75
  Term:           3
  Servers:        
    - node0 (127.0.0.1:8201) voter
    - node1 (127.0.0.1:8203) nonvoter
    - node2 (127.0.0.1:8205) nonvoter

─── State Data ───
  Total Keys:     47

─── Top-level Key Segments ───
  core            21
  logical         21
  sys             5

─── Largest Keys ───
     2.8 kB  logical/1b5aa0b3-2d58-b1d4-9dc9-f0d3b7efd8f3/config/issuer/9d93432a-4d92-aae3-729a-e450c43af295
     2.7 kB  sys/policy/default
     1.8 kB  logical/1b5aa0b3-2d58-b1d4-9dc9-f0d3b7efd8f3/config/key/639999ab-686e-fcfc-cee1-6a6b65a0ddaf
     1.7 kB  core/raft/tls
     1.5 kB  core/leader/b566a37f-48cb-3b33-23da-86450d0d559e
      908 B  logical/99c94a79-1bfb-920e-0393-bea341cc4b3b/b5e7a2fa-37ff-de14-7d3c-21c86a161649/policy/metadata
      836 B  logical/1b5aa0b3-2d58-b1d4-9dc9-f0d3b7efd8f3/certs/2f-ae-62-59-76-a8-e0-4e-df-2f-39-bd-27-a4-cc-b8-04-e0-42-18
      574 B  logical/99c94a79-1bfb-920e-0393-bea341cc4b3b/b5e7a2fa-37ff-de14-7d3c-21c86a161649/archive/metadata
      535 B  core/wrapping/jwtkey
      528 B  sys/token/id/h7c6870f38117fbc2f7dd45c1fb05b25acfee8b0d9e39c3e0830bb711f34908b2

  Index            Raft log index at which this snapshot was taken. [meta.json]
  Term             Raft term at the time of snapshot. [meta.json]
  Servers          Cluster members: voter=participates in elections/quorum, nonvoter=replica only. [meta.json]
  Checksums        SHA-256 integrity verification of archive contents. [SHA256SUMS]
  Total Keys       Number of key/value entries in the FSM state dump. [state.bin]
  Top-level segments correspond to subsystems (core/, sys/, logical/) and their key counts. [state.bin]
  Largest keys shows top 10 entries by encrypted value size. [state.bin]
```

## raft-inspector snapshot --prefix

List snapshot keys matching a prefix.

```console
$ ./raft-inspector snapshot testdata/backup.snap --prefix sys/policy/
─── Keys matching prefix: sys/policy/ ───
sys/policy/default  (2.7 kB)
sys/policy/response-wrapping  (347 B)

  Keys are plaintext storage paths from the snapshot state; values are AES-GCM encrypted. [state.bin]
  Size shown after each key is the encrypted (ciphertext) size, not the plaintext size. [state.bin]
```

Decrypt values in the snapshot for keys matching a prefix.

```console
$ ./raft-inspector snapshot testdata/backup.snap \
    --prefix sys/policy/ --unseal-key-file testdata/init.json
─── Keys matching prefix: sys/policy/ ───
sys/policy/default  (2.7 kB)
  {"Version":2,"DataVersion":1,"CASRequired":false,"Raw":"\n# Allow tokens to look up their own properties\npath \"auth/token/lookup-self\" {\n    capabilities = [\"read\"]\n}\n\n# Allow tokens to renew themselves\npath \"auth/token/renew-self\" {\n    capab [...truncated, 2.7 kB total]
sys/policy/response-wrapping  (347 B)
  {"Version":2,"DataVersion":1,"CASRequired":false,"Raw":"\npath \"cubbyhole/response\" {\n    capabilities = [\"create\", \"read\"]\n}\n\npath \"sys/wrapping/unwrap\" {\n    capabilities = [\"update\"]\n}\n","Templated":false,"Type":0,"Expiration":"0001-01- [...truncated, 314 B total]

  Keys are plaintext storage paths from the snapshot state; values are AES-GCM encrypted. [state.bin]
  Size shown after each key is the encrypted (ciphertext) size, not the plaintext size. [state.bin]
  --unseal-key-file decrypts values using the keyring derived from the unseal key. [state.bin]
```

