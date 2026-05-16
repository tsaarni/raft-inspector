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
```

```console
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
```

```console
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
    ghcr.io/openbao/openbao:2.5.3 server -config=/host/node0.hcl
e9f6791c176adb69f991eb964511a4b8ecd3992c80588e49690c0dae149dcd3a
```

```console
$ docker run -d --name bao-node1 \
    --network host \
    --user $(id -u):$(id -g) \
    -v $PWD/testdata:/host \
    ghcr.io/openbao/openbao:2.5.3 server -config=/host/node1.hcl
41b32ffbe29a3fe16801bf322249cb699de2f8c9c1e1bae4635e5e493c2bb9fa
```

```console
$ docker run -d --name bao-node2 \
    --network host \
    --user $(id -u):$(id -g) \
    -v $PWD/testdata:/host \
    ghcr.io/openbao/openbao:2.5.3 server -config=/host/node2.hcl
e21eec6c0f441abf9820c751578017e624e421c77d1cca7c110a8339dd17d697
```

Wait for node0 to be ready.

### Initialize and unseal

Initialize the cluster on node0 with a single unseal key (for simplicity).

```console
$ docker exec -e BAO_ADDR=http://127.0.0.1:8200 bao-node0 \
    bao operator init -key-shares=1 -key-threshold=1 -format=json > testdata/init.json
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
```

```console
$ docker exec -e BAO_ADDR=http://127.0.0.1:8204 bao-node2 \
    bao operator raft join http://127.0.0.1:8200 > /dev/null
```

Unseal node1 and node2.

```console
$ docker exec -e BAO_ADDR=http://127.0.0.1:8202 bao-node1 \
    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json) > /dev/null
```

```console
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
```

```console
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao secrets tune -max-lease-ttl=87600h pki
Success! Tuned the secrets engine at: pki/
```

```console
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
```

```console
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao kv put secret/myapp/config \
    endpoint=https://api.example.com api_key=secret > /dev/null
```

```console
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao kv put secret/myapp/config \
    endpoint=https://api.example.com api_key=updated > /dev/null
```

```console
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao kv put secret/myapp/credentials \
    username=admin password=mypassword > /dev/null
```

```console
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
```

```console
$ for i in $(seq 1 5); do docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao kv put tmp/$i value=$(head -c 16384 /dev/urandom | base64 -w0); done > /dev/null
```

```console
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
  File Size:          16801792 bytes (16.0 MB)
  DB Logical Size:    327680 bytes (0.3 MB)
  Page Size:          4096 bytes
  Free Pages:         23 (94208 bytes, 0.6%)
  Pending Pages:      0
  Freelist In-Use:    0 bytes
  Space Efficiency:   1.4% (0.2 MB live data)
  Bucket "conf":      3 keys, depth 1, branch 0% leaf 0% utilization
  Bucket "logs":      75 keys, depth 2, branch 13% leaf 73% utilization
  Total:              78 keys, branch 13% leaf 73% utilization
  Integrity Check:    OK

─── BoltDB Stats: vault.db ───
  File Size:          16801792 bytes (16.0 MB)
  DB Logical Size:    389120 bytes (0.4 MB)
  Page Size:          4096 bytes
  Free Pages:         81 (331776 bytes, 2.0%)
  Pending Pages:      0
  Freelist In-Use:    0 bytes
  Space Efficiency:   0.3% (0.1 MB live data)
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
```

```console
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
  File Size:          16801792 bytes (16.0 MB)
  DB Logical Size:    327680 bytes (0.3 MB)
  Free Pages:         22 (90112 bytes, 0.5%)
  Space Efficiency:   1.4% (0.2 MB live data)
─── BoltDB Stats: vault.db ───
  File Size:          131072 bytes (0.1 MB)
  DB Logical Size:    77824 bytes (0.1 MB)
  Free Pages:         3 (12288 bytes, 9.4%)
  Space Efficiency:   50.0% (0.1 MB live data)
```

## raft-inspector log

Show log entries with decrypted values. The `put` operations reveal the actual stored data.

```console
$ ./raft-inspector -d testdata/node0 log -n 3 \
    --decrypt --unseal-key-file testdata/init.json
─── raft/raft.db logs bucket (entries 1 to 80, showing 78 to 80) ───

─── Index 78 (raft/raft.db logs/78) ───
  Index:      78
  Term:       3
  Type:       LogCommand
  AppendedAt: 2026-05-16 08:45:25.732639827 +0000 UTC  (+0s)
  Operations:
    [op=4/restoreCallback]   (0 bytes)

─── Index 79 (raft/raft.db logs/79) ───
  Index:      79
  Term:       3
  Type:       LogCommand
  AppendedAt: 2026-05-16 08:45:30.746260059 +0000 UTC  (+5.014s)
  Operations:
    [op=2/put] core/lock  (36 bytes)
      29d3eec7-e153-1670-e0e1-1079dc64516e

─── Index 80 (raft/raft.db logs/80) ───
  Index:      80
  Term:       3
  Type:       LogCommand
  AppendedAt: 2026-05-16 08:45:30.773829616 +0000 UTC  (+5.041s)
  Operations:
    [op=2/put] core/leader/29d3eec7-e153-1670-e0e1-1079dc64516e  (1506 bytes)
      {"redirect_addr":"http://127.0.0.1:8200","cluster_addr":"https://127.0.0.1:8201","cluster_cert":"MIICezCCAdygAwIBAgIIXqI4vEFOmhUwCgYIKoZIzj0EAwQwMjEwMC4GA1UEAxMnZnctYmRkOGIyYzgtYTI3NC1kMzgzLWUwNmEtMTA2YzBlZWFlMGQxMCAXDTI2MDUxNjA4NDQ0OVoYDzIwNTYwNTE1MjA0NTE [...truncated, 1473 bytes total]


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
  Time Range:         0001-01-01 00:00:00 +0000 UTC → 2026-05-16 08:45:30.773829616 +0000 UTC
  Entry Count:        80
  Total Size:         153464 bytes
  Average Size:       1918 bytes
  Max Size:           23075 bytes

─── Operation Distribution ───
  put                 72
  verifyRead          46
  delete              27
  beginTx             18
  commitTx            18
  verifyList          7
  restoreCallback     1

─── Hot Keys (top 10) ───
                                                              37
  core/mounts                                                 14
  core/mounts/322760bb-5b0a-e04d-0a66-de097b4de699            6
  logical/7805e7c8-84f0-c698-2803-c9e6324927d1/c7f9747a-7ed6-baec-a101-2121da8299d9/metadata/5kL41q7dCVRP8zqETa5DCbQS1GAUQndszs2DEeEezovYzc5L4mvC72DUE6pnAx/p0EgU8Sj813PYqdepcTlvKOrHZj9Fkwd2N1zRapQGsggzuz1tFWqGpdzb9KYKuvhVtH4
  core/mounts/599e15d8-634c-25ae-3478-f0f77d3c3674            4
  logical/7805e7c8-84f0-c698-2803-c9e6324927d1/c7f9747a-7ed6-baec-a101-2121da8299d9/metadata/5kL41q7dCVRP8zqETa5DCbQS1GAUQndszs2DEeEezovYzc5L4mvC72DUE6pnAx/1TfFvMlr5n8tAzgPrlUbsTBHKPEY2zgmM8mrrZW5qW3J0OeOIkYqTvYpu63aIQtDsu9AaewAM4
  logical/599e15d8-634c-25ae-3478-f0f77d3c3674/crls/config    4
  logical/322760bb-5b0a-e04d-0a66-de097b4de699/17978e10-2583-d300-af24-44b3ec5d7ee8/versions/901/af06fa49e6e0c268b52db2d0ad222eb3eb46c147fb647765754389ddf68b13
  logical/322760bb-5b0a-e04d-0a66-de097b4de699/17978e10-2583-d300-af24-44b3ec5d7ee8/salt3
  logical/322760bb-5b0a-e04d-0a66-de097b4de699/17978e10-2583-d300-af24-44b3ec5d7ee8/versions/6c6/3589cf8f92e3fbfc32510a3b4e3da109bd03733a944082b26d4f68e0105293

  Time Range         Wall-clock range from oldest to newest log entry's AppendedAt timestamp. [raft/raft.db]
  Entry Count        Total number of log entries in the retained log. [raft/raft.db]
  Total/Avg/Max Size Byte sizes of log entry Data payloads (encrypted operations). [raft/raft.db]
  Op Distribution    Count of each operation type (put, delete, etc.) across all log entries. [raft/raft.db]
  Hot Keys           Storage paths most frequently written to; helps identify write-heavy workloads. [raft/raft.db]
```

## raft-inspector fsm

Show total key count in the FSM data store (`vault.db`).

```console
$ ./raft-inspector -d testdata/node0 fsm
Total keys in data bucket: 47

  Keys are plaintext storage paths from the vault.db data bucket; values are AES-GCM encrypted. [vault.db]
  Top-level segments correspond to subsystems (core/, sys/, logical/) and their key counts. [vault.db]
```

## raft-inspector fsm --top

Show top-level key path segments with counts.

```console
$ ./raft-inspector -d testdata/node0 fsm --top
─── Top-level Key Segments ───
  core                                    21
  logical                                 21
  sys                                     5

  Keys are plaintext storage paths from the vault.db data bucket; values are AES-GCM encrypted. [vault.db]
  Top-level segments correspond to subsystems (core/, sys/, logical/) and their key counts. [vault.db]
```

## raft-inspector fsm --prefix

List FSM keys matching a prefix.

```console
$ ./raft-inspector -d testdata/node0 fsm --prefix sys/policy/
─── Keys matching prefix: sys/policy/ ───
sys/policy/default
sys/policy/response-wrapping

  Keys are plaintext storage paths from the vault.db data bucket; values are AES-GCM encrypted. [vault.db]
  Top-level segments correspond to subsystems (core/, sys/, logical/) and their key counts. [vault.db]
```

Show decrypted values for keys matching a prefix.

```console
$ ./raft-inspector -d testdata/node0 fsm --prefix sys/policy/ \
    --decrypt --unseal-key-file testdata/init.json
─── Keys matching prefix: sys/policy/ ───
sys/policy/default
  {"Version":2,"DataVersion":1,"CASRequired":false,"Raw":"\n# Allow tokens to look up their own properties\npath \"auth/token/lookup-self\" {\n    capabilities = [\"read\"]\n}\n\n# Allow tokens to renew themselves\npath \"auth/token/renew-self\" {\n    capab [...truncated, 2651 bytes total]
sys/policy/response-wrapping
  {"Version":2,"DataVersion":1,"CASRequired":false,"Raw":"\npath \"cubbyhole/response\" {\n    capabilities = [\"create\", \"read\"]\n}\n\npath \"sys/wrapping/unwrap\" {\n    capabilities = [\"update\"]\n}\n","Templated":false,"Type":0,"Expiration":"0001-01- [...truncated, 315 bytes total]

  Keys are plaintext storage paths from the vault.db data bucket; values are AES-GCM encrypted. [vault.db]
  Top-level segments correspond to subsystems (core/, sys/, logical/) and their key counts. [vault.db]
  --decrypt decrypts values using the keyring derived from the unseal key. [vault.db]
```

## raft-inspector snapshot

Inspect the snapshot archive metadata.

```console
$ ./raft-inspector -d testdata/node0 snapshot testdata/backup.snap
─── Snapshot Metadata ───
  Index:          75
  Term:           3
  Servers:        
    - node0 (127.0.0.1:8201) voter
    - node1 (127.0.0.1:8203) nonvoter
    - node2 (127.0.0.1:8205) nonvoter

─── State Data ───
  Total Keys:     47
  Total Size:     20945 bytes

  Index            Raft log index at which this snapshot was taken. [meta.json]
  Term             Raft term at the time of snapshot. [meta.json]
  Servers          Cluster members: voter=participates in elections/quorum, nonvoter=replica only. [meta.json]
  Checksums        SHA-256 integrity verification of archive contents. [SHA256SUMS]
  Total Keys       Number of key/value entries in the FSM state dump. [state.bin]
  Total Size       Sum of all value bytes (encrypted); does not include key path sizes. [state.bin]
  --keys           Print all key paths; add --decrypt --unseal-key-file to show decrypted values.
```

## raft-inspector snapshot --keys

List all key paths stored in the snapshot.

```console
$ ./raft-inspector -d testdata/node0 snapshot testdata/backup.snap --keys
─── Snapshot Metadata ───
  Index:          75
  Term:           3
  Servers:        
    - node0 (127.0.0.1:8201) voter
    - node1 (127.0.0.1:8203) nonvoter
    - node2 (127.0.0.1:8205) nonvoter

─── State Data ───
core/audit
core/auth/e49a6f0a-54e6-3f69-6a9a-9210c4149731
core/cluster/local/info
core/hsm/barrier-unseal-keys
core/index-header-hmac-key
core/initialize-lock
core/keyring
core/leader/29d3eec7-e153-1670-e0e1-1079dc64516e
core/local-audit
core/local-mounts/f03e35f2-5446-a2bf-c042-ffe00861f251
core/lock
core/mounts/1bac93a0-b10b-5a7b-41c1-86d3b0de9bfb
core/mounts/599e15d8-634c-25ae-3478-f0f77d3c3674
core/mounts/761574d8-c5c0-f7c0-e17b-8b8d6a0b40eb
core/mounts/7805e7c8-84f0-c698-2803-c9e6324927d1
core/raft/tls
core/root-key
core/seal-config
core/shamir-kek
core/versions/2.5.3
core/wrapping/jwtkey
logical/599e15d8-634c-25ae-3478-f0f77d3c3674/certs/4f-c8-37-fe-54-2b-65-df-6b-8a-20-d9-39-dd-6c-0a-43-9d-43-84
logical/599e15d8-634c-25ae-3478-f0f77d3c3674/config/issuer/c1065386-2c18-32ba-8ed6-1de9b6daf01e
logical/599e15d8-634c-25ae-3478-f0f77d3c3674/config/issuers
logical/599e15d8-634c-25ae-3478-f0f77d3c3674/config/key/aa16e1b9-a629-b013-b7ee-224169ecedbe
logical/599e15d8-634c-25ae-3478-f0f77d3c3674/config/keys
logical/599e15d8-634c-25ae-3478-f0f77d3c3674/config/legacyMigrationBundleLog
logical/599e15d8-634c-25ae-3478-f0f77d3c3674/crls/12846e45-da2e-b287-5edb-ece9497726e3
logical/599e15d8-634c-25ae-3478-f0f77d3c3674/crls/12846e45-da2e-b287-5edb-ece9497726e3-delta
logical/599e15d8-634c-25ae-3478-f0f77d3c3674/crls/config
logical/761574d8-c5c0-f7c0-e17b-8b8d6a0b40eb/oidc_provider/assignment/allow_all
logical/761574d8-c5c0-f7c0-e17b-8b8d6a0b40eb/oidc_provider/provider/default
logical/761574d8-c5c0-f7c0-e17b-8b8d6a0b40eb/oidc_tokens/named_keys/default
logical/7805e7c8-84f0-c698-2803-c9e6324927d1/c7f9747a-7ed6-baec-a101-2121da8299d9/archive/metadata
logical/7805e7c8-84f0-c698-2803-c9e6324927d1/c7f9747a-7ed6-baec-a101-2121da8299d9/metadata/5kL41q7dCVRP8zqETa5DCbQS1GAUQndszs2DEeEezovYzc5L4mvC72DUE6pnAx/1TfFvMlr5n8tAzgPrlUbsTBHKPEY2zgmM8mrrZW5qW3J0OeOIkYqTvYpu63aIQtDsu9AaewAM
logical/7805e7c8-84f0-c698-2803-c9e6324927d1/c7f9747a-7ed6-baec-a101-2121da8299d9/metadata/5kL41q7dCVRP8zqETa5DCbQS1GAUQndszs2DEeEezovYzc5L4mvC72DUE6pnAx/p0EgU8Sj813PYqdepcTlvKOrHZj9Fkwd2N1zRapQGsggzuz1tFWqGpdzb9KYKuvhVtH
logical/7805e7c8-84f0-c698-2803-c9e6324927d1/c7f9747a-7ed6-baec-a101-2121da8299d9/policy/metadata
logical/7805e7c8-84f0-c698-2803-c9e6324927d1/c7f9747a-7ed6-baec-a101-2121da8299d9/salt
logical/7805e7c8-84f0-c698-2803-c9e6324927d1/c7f9747a-7ed6-baec-a101-2121da8299d9/upgrading
logical/7805e7c8-84f0-c698-2803-c9e6324927d1/c7f9747a-7ed6-baec-a101-2121da8299d9/versions/066/1e6edec632800534d4ef6a86ef5c8f6ecaa62455f8fadef4155bb24100e32
logical/7805e7c8-84f0-c698-2803-c9e6324927d1/c7f9747a-7ed6-baec-a101-2121da8299d9/versions/307/da6c1e733eba9bcde235371572a6aa827408b3c97e4aa3af61e6101455df2
logical/7805e7c8-84f0-c698-2803-c9e6324927d1/c7f9747a-7ed6-baec-a101-2121da8299d9/versions/af5/b6e4581ab6723b8ac328ac44b0900bd76ee0f8ea3667d69cee48fb065ac33
sys/policy/default
sys/policy/response-wrapping
sys/token/accessor/60ff7123d2a2bdf872622434ca7bf436ef691860
sys/token/id/hf55bd1fee29533ee1a7c3a671c237554005be7cc735b4f06801abdf08abcce62
sys/token/salt
  Total Keys:     47
  Total Size:     20945 bytes

  Index            Raft log index at which this snapshot was taken. [meta.json]
  Term             Raft term at the time of snapshot. [meta.json]
  Servers          Cluster members: voter=participates in elections/quorum, nonvoter=replica only. [meta.json]
  Checksums        SHA-256 integrity verification of archive contents. [SHA256SUMS]
  Total Keys       Number of key/value entries in the FSM state dump. [state.bin]
  Total Size       Sum of all value bytes (encrypted); does not include key path sizes. [state.bin]
  --keys           Print all key paths; add --decrypt --unseal-key-file to show decrypted values.
```

Decrypt values in the snapshot.

```console
$ ./raft-inspector -d testdata/node0 snapshot testdata/backup.snap \
    --keys --decrypt --unseal-key-file testdata/init.json --limit 5
─── Snapshot Metadata ───
  Index:          75
  Term:           3
  Servers:        
    - node0 (127.0.0.1:8201) voter
    - node1 (127.0.0.1:8203) nonvoter
    - node2 (127.0.0.1:8205) nonvoter

─── State Data ───
core/audit
  [hex] 471f8b08000000000002ffaa562aa92c4855b2524a2c4dc92c51d2514acd2b29ca4c2d56b2ca2bcdc9a9e502040000ffff9bcf028720000000
core/auth/e49a6f0a-54e6-3f69-6a9a-9210c4149731
  {"table":"auth","path":"token/","type":"token","description":"token based credentials","uuid":"e49a6f0a-54e6-3f69-6a9a-9210c4149731","backend_aware_uuid":"16150077-64aa-7565-0c92-f99f516f0146","accessor":"auth_token_dfeec717","config":{},"options":null,"lo [...truncated, 308 bytes total]
core/cluster/local/info
  {"name":"vault-cluster-7edf984c","id":"e6c1dc42-2ba1-4648-d67f-e36be012e944"}
core/hsm/barrier-unseal-keys
  [decrypt error: skipped: encrypted with seal key, not barrier (protobuf-wrapped BlobInfo)]
core/index-header-hmac-key
  f0bc489f-a88d-5f46-37a4-d26be06ec5ab

  [output limited to 5 entries, continuing count...]
  Total Keys:     47
  Total Size:     20945 bytes

  Index            Raft log index at which this snapshot was taken. [meta.json]
  Term             Raft term at the time of snapshot. [meta.json]
  Servers          Cluster members: voter=participates in elections/quorum, nonvoter=replica only. [meta.json]
  Checksums        SHA-256 integrity verification of archive contents. [SHA256SUMS]
  Total Keys       Number of key/value entries in the FSM state dump. [state.bin]
  Total Size       Sum of all value bytes (encrypted); does not include key path sizes. [state.bin]
  --keys           Print all key paths; add --decrypt --unseal-key-file to show decrypted values.
```

