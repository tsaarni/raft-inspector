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
9472af21e601f70bf79fd9bcd777b687a5ce20ac49db684a6bfcb258832aea3f
```

```console
$ docker run -d --name bao-node1 \
    --network host \
    --user $(id -u):$(id -g) \
    -v $PWD/testdata:/host \
    ghcr.io/openbao/openbao:2.5.3 server -config=/host/node1.hcl
79012679d29fbb55ba1a7688ecfcd9d1a70a04bc6bf5a080626063f2a9da72ff
```

```console
$ docker run -d --name bao-node2 \
    --network host \
    --user $(id -u):$(id -g) \
    -v $PWD/testdata:/host \
    ghcr.io/openbao/openbao:2.5.3 server -config=/host/node2.hcl
ed287f488ebd60becc441400ac17891eb51806cb8d25b6e03eb2dcc411b90c2a
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
    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json)
Key                     Value
---                     -----
Seal Type               shamir
Initialized             true
Sealed                  false
Total Shares            1
Threshold               1
Version                 2.5.3
Build Date              2026-04-20T19:28:29Z
Storage Type            raft
Cluster Name            vault-cluster-e6b3bd6e
Cluster ID              2604a01b-beef-765e-df47-dac1891aa3ff
HA Enabled              true
HA Cluster              n/a
HA Mode                 standby
Active Node Address     <none>
Raft Committed Index    26
Raft Applied Index      26
```

Join node1 and node2 to the cluster, then unseal them.

```console
$ docker exec -e BAO_ADDR=http://127.0.0.1:8202 bao-node1 \
    bao operator raft join http://127.0.0.1:8200
Key       Value
---       -----
Joined    true
```

```console
$ docker exec -e BAO_ADDR=http://127.0.0.1:8204 bao-node2 \
    bao operator raft join http://127.0.0.1:8200
Key       Value
---       -----
Joined    true
```

Unseal node1 and node2.

```console
$ docker exec -e BAO_ADDR=http://127.0.0.1:8202 bao-node1 \
    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json)
Key                Value
---                -----
Seal Type          shamir
Initialized        true
Sealed             true
Total Shares       1
Threshold          1
Unseal Progress    0/1
Unseal Nonce       n/a
Version            2.5.3
Build Date         2026-04-20T19:28:29Z
Storage Type       raft
HA Enabled         true
```

```console
$ docker exec -e BAO_ADDR=http://127.0.0.1:8204 bao-node2 \
    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json)
Key                Value
---                -----
Seal Type          shamir
Initialized        true
Sealed             true
Total Shares       1
Threshold          1
Unseal Progress    0/1
Unseal Nonce       n/a
Version            2.5.3
Build Date         2026-04-20T19:28:29Z
Storage Type       raft
HA Enabled         true
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
    common_name='Test Root CA' ttl=87600h
-----BEGIN CERTIFICATE-----
MIIDHzCCAgegAwIBAgIUNS3PyjKQGktyiO8SILxr4de9ZY8wDQYJKoZIhvcNAQEL
BQAwFzEVMBMGA1UEAxMMVGVzdCBSb290IENBMB4XDTI2MDUxNTE2MDUwNFoXDTM2
MDUxMjE2MDUzNFowFzEVMBMGA1UEAxMMVGVzdCBSb290IENBMIIBIjANBgkqhkiG
9w0BAQEFAAOCAQ8AMIIBCgKCAQEAyZHChNzujEAr0JlgbZUzzuGDdXTubQbKeIzh
Xghh7RaqUj3LF3KGNQ9wF+GdELdL/RMa/e6K3QaZ4srvm1iCQGVzvCzpeKZVrSOc
L+qUScN9Y/VE5ZSLFvvZBoTMuHEGogrtmfegdO58+S8uB3Ps//mA+8kgng2f7E/M
WutZnW8gNAY2fOmhc559d6deFw5zwonDhwY2xd3Yr0/V1YJgl2AXYWueLBykoFMb
qPkYutzgQzuS7hH5WG7iMBTstMXlwB5BnZDimz+0oJKHnhq6Q3Iq4mXTeTeTkief
q5cMXgbSftauQH66QQ0dOlWxVCGoOJwo0yTvQD1aLa+bjpwK6wIDAQABo2MwYTAO
BgNVHQ8BAf8EBAMCAQYwDwYDVR0TAQH/BAUwAwEB/zAdBgNVHQ4EFgQUtMtngSJt
0dljArvlrTEfmDIE6LgwHwYDVR0jBBgwFoAUtMtngSJt0dljArvlrTEfmDIE6Lgw
DQYJKoZIhvcNAQELBQADggEBADtmvmlEWwUhsnbG3VyjXOtDwDtHSRJolWsWQxz1
HJyfPmCExtGOJeFbZhursYO+OaxUZk8anl57b9GYXVlxzij6YmRz51b1DcUbiTPM
LAVv6HYlNa2hqlQB+8MqXoQ0Buz44P/UFbj6fv2o8NLhltcmAStzl+EvWZq4FGxY
1yzylxxPdAQvILUcpc31eaEDVV8V2N46y7eYnHPbMGjMSXCefZQdfRgBuljheujG
TGYWI/FHhg0KNn6ELJk1Z/1SMVdTFdAIdhsZRB4NA6iiJWxrxmvr+tENm5ckAqWn
plDkMtnwaAj4P7WWaM+WQVhWoX34Gtu9Eb7W5GMRIlyrzyk=
-----END CERTIFICATE-----
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
    endpoint=https://api.example.com api_key=secret
====== Secret Path ======
secret/data/myapp/config

======= Metadata =======
Key                Value
---                -----
created_time       2026-05-15T16:05:35.064467449Z
custom_metadata    <nil>
deletion_time      n/a
destroyed          false
version            1
```

```console
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao kv put secret/myapp/config \
    endpoint=https://api.example.com api_key=updated
====== Secret Path ======
secret/data/myapp/config

======= Metadata =======
Key                Value
---                -----
created_time       2026-05-15T16:05:35.22826047Z
custom_metadata    <nil>
deletion_time      n/a
destroyed          false
version            2
```

```console
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao kv put secret/myapp/credentials \
    username=admin password=mypassword
======== Secret Path ========
secret/data/myapp/credentials

======= Metadata =======
Key                Value
---                -----
created_time       2026-05-15T16:05:35.389262544Z
custom_metadata    <nil>
deletion_time      n/a
destroyed          false
version            1
```

```console
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao kv delete secret/myapp/credentials
Success! Data deleted (if it existed) at: secret/data/myapp/credentials
```

Take a raft snapshot for later inspection.

```console
$ docker exec \
    -e BAO_ADDR=http://127.0.0.1:8200 \
    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \
    bao-node0 bao operator raft snapshot save /host/backup.snap
```

## raft-inspector status

Combined health overview reading both `raft/raft.db` and `vault.db`.

```console
$ ./raft-inspector -d testdata/node0 status
─── raft/raft.db stable store ───
  Current Term:       3
  First Log Index:    1
  Last Log Index:     49
  Entry Count:        49
  Last Vote Cand:     127.0.0.1:8201
  Last Vote Term:     3

─── vault.db config bucket ───
  Applied Index:      49  (config/latest_indexes)
  Applied Term:       3  (config/latest_indexes)
  Config Index:       31  (config/latest_config)
  Servers:              (config/latest_config)
    - node0 (127.0.0.1:8201) voter
    - node1 (127.0.0.1:8203) nonvoter
    - node2 (127.0.0.1:8205) nonvoter
  Desired Suffrage:   voter  (config/local_node_config)

─── Computed ───
  Unapplied Entries:  0
  Trailing Entries:   48
  Snapshot Index:     0

─── BoltDB Stats: raft/raft.db ───
  File Size:          16801792 bytes (16.0 MB)
  DB Logical Size:    110592 bytes (0.1 MB)
  Page Size:          4096 bytes
  Free Pages:         0 (0 bytes, 0.0%)
  Pending Pages:      0
  Freelist In-Use:    0 bytes

─── BoltDB Stats: vault.db ───
  File Size:          16801792 bytes (16.0 MB)
  DB Logical Size:    65536 bytes (0.1 MB)
  Page Size:          4096 bytes
  Free Pages:         0 (0 bytes, 0.0%)
  Pending Pages:      0
  Freelist In-Use:    0 bytes

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
  DB Logical Size    Pages allocated by BoltDB (file may be larger due to OS allocation). [bolt.Tx.Size]
  Page Size          BoltDB page size; all allocations are in multiples of this. [bolt.DB.Info]
  Free Pages         Pages released by deletes but not yet returned to OS; reused for future writes. [bolt.DB.Stats]
  Pending Pages      Pages freed in current transaction, not yet available for reuse. [bolt.DB.Stats]
  Freelist In-Use    Bytes used by BoltDB's internal freelist tracking structure. [bolt.DB.Stats]
```

## raft-inspector log

Show log entries with decrypted values. The `put` operations reveal the actual stored data.

```console
$ ./raft-inspector -d testdata/node0 log -n 3 \
    --decrypt --unseal-key-file testdata/init.json
─── raft/raft.db logs bucket (entries 1 to 49, showing 47 to 49) ───

─── Index 47 (raft/raft.db logs/47) ───
  Index:      47
  Term:       3
  Type:       LogCommand
  AppendedAt: 2026-05-15 16:05:35.228302664 +0000 UTC  (+0s)
  Operations:
    [op=64/beginTx]   (8 bytes)
    [op=16/verifyRead] logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/metadata/5kKZIWyRbsIGBggDOSl9lJnc1sI4ldZxBsnbSoENn5Os3gb1nHokWTuGXhkUfy/p0CoAuWb2spN6Mk1TGccg1qQPzIxqABAgLK2pYFR6huHrRlL9Y9R9VJCrsHcnhdSf9j  (49 bytes)
    [op=16/verifyRead] logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/versions/5d4/9b1c3fdea26a83d59249b950d942f6b77b35df76d4bedcc5b58d80143d51d  (49 bytes)
    [op=2/put] logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/versions/5d4/9b1c3fdea26a83d59249b950d942f6b77b35df76d4bedcc5b58d80143d51d  (106 bytes)
      {"api_key":"updated","endpoint":"https://api.example.com"}
    [op=2/put] logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/metadata/5kKZIWyRbsIGBggDOSl9lJnc1sI4ldZxBsnbSoENn5Os3gb1nHokWTuGXhkUfy/p0CoAuWb2spN6Mk1TGccg1qQPzIxqABAgLK2pYFR6huHrRlL9Y9R9VJCrsHcnhdSf9j  (113 bytes)
      myapp/config
    [op=128/commitTx]   (0 bytes)

─── Index 48 (raft/raft.db logs/48) ───
  Index:      48
  Term:       3
  Type:       LogCommand
  AppendedAt: 2026-05-15 16:05:35.389308315 +0000 UTC  (+161ms)
  Operations:
    [op=64/beginTx]   (8 bytes)
    [op=16/verifyRead] logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/metadata/5kKZIWyRbsIGBggDOSl9lJnc1sI4ldZxBsnbSoENn5Os3gb1nHokWTuGXhkUfy/1TfA4Ai4lRtB8Y417pFSHyjwVrVXb5nbsHu5OipMgafxq2Wal1j0uhFYqRswJYsOTvGFDkCpo  (49 bytes)
    [op=16/verifyRead] logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/versions/3fc/9a14c43f68d3307e177186426f0fb82e8c96f14be101ac88d022e6b8e07da  (49 bytes)
    [op=2/put] logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/versions/3fc/9a14c43f68d3307e177186426f0fb82e8c96f14be101ac88d022e6b8e07da  (93 bytes)
      {"password":"mypassword","username":"admin"}
    [op=2/put] logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/metadata/5kKZIWyRbsIGBggDOSl9lJnc1sI4ldZxBsnbSoENn5Os3gb1nHokWTuGXhkUfy/1TfA4Ai4lRtB8Y417pFSHyjwVrVXb5nbsHu5OipMgafxq2Wal1j0uhFYqRswJYsOTvGFDkCpo  (102 bytes)
      myapp/credentials
    [op=128/commitTx]   (0 bytes)

─── Index 49 (raft/raft.db logs/49) ───
  Index:      49
  Term:       3
  Type:       LogCommand
  AppendedAt: 2026-05-15 16:05:35.543361664 +0000 UTC  (+315ms)
  Operations:
    [op=64/beginTx]   (8 bytes)
    [op=16/verifyRead] logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/metadata/5kKZIWyRbsIGBggDOSl9lJnc1sI4ldZxBsnbSoENn5Os3gb1nHokWTuGXhkUfy/1TfA4Ai4lRtB8Y417pFSHyjwVrVXb5nbsHu5OipMgafxq2Wal1j0uhFYqRswJYsOTvGFDkCpo  (49 bytes)
    [op=2/put] logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/metadata/5kKZIWyRbsIGBggDOSl9lJnc1sI4ldZxBsnbSoENn5Os3gb1nHokWTuGXhkUfy/1TfA4Ai4lRtB8Y417pFSHyjwVrVXb5nbsHu5OipMgafxq2Wal1j0uhFYqRswJYsOTvGFDkCpo  (116 bytes)
      myapp/credentials
    [op=128/commitTx]   (0 bytes)


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
  Time Range:         0001-01-01 00:00:00 +0000 UTC → 2026-05-15 16:05:35.543361664 +0000 UTC
  Entry Count:        49
  Total Size:         32103 bytes
  Average Size:       655 bytes
  Max Size:           7785 bytes

─── Operation Distribution ───
  put                 53
  verifyRead          29
  commitTx            10
  beginTx             10
  delete              9
  verifyList          7

─── Hot Keys (top 10) ───
                                                              20
  core/mounts                                                 8
  logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/metadata/5kKZIWyRbsIGBggDOSl9lJnc1sI4ldZxBsnbSoENn5Os3gb1nHokWTuGXhkUfy/p0CoAuWb2spN6Mk1TGccg1qQPzIxqABAgLK2pYFR6huHrRlL9Y9R9VJCrsHcnhdSf9j4
  logical/d7e05716-9efc-adff-11b1-f6633d9d680a/crls/config    4
  core/mounts/d7e05716-9efc-adff-11b1-f6633d9d680a            4
  logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/metadata/5kKZIWyRbsIGBggDOSl9lJnc1sI4ldZxBsnbSoENn5Os3gb1nHokWTuGXhkUfy/1TfA4Ai4lRtB8Y417pFSHyjwVrVXb5nbsHu5OipMgafxq2Wal1j0uhFYqRswJYsOTvGFDkCpo4
  logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/versions/dfb/649f59a149dd2eedb47ae4be8af465b49a47dfb22ebdbc784185b6b3c051c2
  logical/d7e05716-9efc-adff-11b1-f6633d9d680a/config/key/b3b0da9e-33d5-a27f-0c68-4b933d2587a82
  logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/versions/5d4/9b1c3fdea26a83d59249b950d942f6b77b35df76d4bedcc5b58d80143d51d2
  core/auth                                                   2

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
  logical                                 21
  core                                    21
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
  Index:          49
  Term:           3
  Servers:        
    - node0 (127.0.0.1:8201) voter
    - node1 (127.0.0.1:8203) nonvoter
    - node2 (127.0.0.1:8205) nonvoter

─── State Data ───
  Total Keys:     47
  Total Size:     20944 bytes

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
  Index:          49
  Term:           3
  Servers:        
    - node0 (127.0.0.1:8201) voter
    - node1 (127.0.0.1:8203) nonvoter
    - node2 (127.0.0.1:8205) nonvoter

─── State Data ───
core/audit
core/auth/376df07a-c9ea-5034-5ed4-e5da5143e9c1
core/cluster/local/info
core/hsm/barrier-unseal-keys
core/index-header-hmac-key
core/initialize-lock
core/keyring
core/leader/a0de0321-a150-9e3d-2cdf-5166128af79b
core/local-audit
core/local-mounts/93d445db-d678-e1e7-a774-d2b1cd53abf9
core/lock
core/mounts/06856671-3db1-5639-5f0c-3a2f61e8bb9a
core/mounts/538d9705-2036-5e1d-d979-29f41480bbb7
core/mounts/94b2019e-8a09-a4c5-b32d-43c96e33c313
core/mounts/d7e05716-9efc-adff-11b1-f6633d9d680a
core/raft/tls
core/root-key
core/seal-config
core/shamir-kek
core/versions/2.5.3
core/wrapping/jwtkey
logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/archive/metadata
logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/metadata/5kKZIWyRbsIGBggDOSl9lJnc1sI4ldZxBsnbSoENn5Os3gb1nHokWTuGXhkUfy/1TfA4Ai4lRtB8Y417pFSHyjwVrVXb5nbsHu5OipMgafxq2Wal1j0uhFYqRswJYsOTvGFDkCpo
logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/metadata/5kKZIWyRbsIGBggDOSl9lJnc1sI4ldZxBsnbSoENn5Os3gb1nHokWTuGXhkUfy/p0CoAuWb2spN6Mk1TGccg1qQPzIxqABAgLK2pYFR6huHrRlL9Y9R9VJCrsHcnhdSf9j
logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/policy/metadata
logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/salt
logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/upgrading
logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/versions/3fc/9a14c43f68d3307e177186426f0fb82e8c96f14be101ac88d022e6b8e07da
logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/versions/5d4/9b1c3fdea26a83d59249b950d942f6b77b35df76d4bedcc5b58d80143d51d
logical/06856671-3db1-5639-5f0c-3a2f61e8bb9a/87559e23-e634-a220-0c14-b9f13e173ceb/versions/dfb/649f59a149dd2eedb47ae4be8af465b49a47dfb22ebdbc784185b6b3c051c
logical/94b2019e-8a09-a4c5-b32d-43c96e33c313/oidc_provider/assignment/allow_all
logical/94b2019e-8a09-a4c5-b32d-43c96e33c313/oidc_provider/provider/default
logical/94b2019e-8a09-a4c5-b32d-43c96e33c313/oidc_tokens/named_keys/default
logical/d7e05716-9efc-adff-11b1-f6633d9d680a/certs/35-2d-cf-ca-32-90-1a-4b-72-88-ef-12-20-bc-6b-e1-d7-bd-65-8f
logical/d7e05716-9efc-adff-11b1-f6633d9d680a/config/issuer/a05f18be-fae1-2bcc-2508-d420d90d1e27
logical/d7e05716-9efc-adff-11b1-f6633d9d680a/config/issuers
logical/d7e05716-9efc-adff-11b1-f6633d9d680a/config/key/b3b0da9e-33d5-a27f-0c68-4b933d2587a8
logical/d7e05716-9efc-adff-11b1-f6633d9d680a/config/keys
logical/d7e05716-9efc-adff-11b1-f6633d9d680a/config/legacyMigrationBundleLog
logical/d7e05716-9efc-adff-11b1-f6633d9d680a/crls/ca327621-8d98-4eab-4d62-e6c1eaa69816
logical/d7e05716-9efc-adff-11b1-f6633d9d680a/crls/ca327621-8d98-4eab-4d62-e6c1eaa69816-delta
logical/d7e05716-9efc-adff-11b1-f6633d9d680a/crls/config
sys/policy/default
sys/policy/response-wrapping
sys/token/accessor/a8268f2e3e892558b105cb9acb1777e6f500cb7b
sys/token/id/h983e679dccc80bf0fa3e1b18f474a4c6a612861fa166e760c53986a2bd801896
sys/token/salt
  Total Keys:     47
  Total Size:     20944 bytes

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
  Index:          49
  Term:           3
  Servers:        
    - node0 (127.0.0.1:8201) voter
    - node1 (127.0.0.1:8203) nonvoter
    - node2 (127.0.0.1:8205) nonvoter

─── State Data ───
core/audit
  [hex] 471f8b08000000000002ffaa562aa92c4855b2524a2c4dc92c51d2514acd2b29ca4c2d56b2ca2bcdc9a9e502040000ffff9bcf028720000000
core/auth/376df07a-c9ea-5034-5ed4-e5da5143e9c1
  {"table":"auth","path":"token/","type":"token","description":"token based credentials","uuid":"376df07a-c9ea-5034-5ed4-e5da5143e9c1","backend_aware_uuid":"162b5d16-b469-f1c6-95fb-8a5d192947e9","accessor":"auth_token_f882f299","config":{},"options":null,"lo [...truncated, 308 bytes total]
core/cluster/local/info
  {"name":"vault-cluster-e6b3bd6e","id":"2604a01b-beef-765e-df47-dac1891aa3ff"}
core/hsm/barrier-unseal-keys
  [decrypt error: no key for term 172770986]
core/index-header-hmac-key
  b58d98e3-22a8-a117-7dbd-828aea37eb49

  [output limited to 5 entries, continuing count...]
  Total Keys:     47
  Total Size:     20944 bytes

  Index            Raft log index at which this snapshot was taken. [meta.json]
  Term             Raft term at the time of snapshot. [meta.json]
  Servers          Cluster members: voter=participates in elections/quorum, nonvoter=replica only. [meta.json]
  Checksums        SHA-256 integrity verification of archive contents. [SHA256SUMS]
  Total Keys       Number of key/value entries in the FSM state dump. [state.bin]
  Total Size       Sum of all value bytes (encrypted); does not include key path sizes. [state.bin]
  --keys           Print all key paths; add --decrypt --unseal-key-file to show decrypted values.
```

