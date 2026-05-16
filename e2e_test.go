//go:build e2e

package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const image = "ghcr.io/openbao/openbao:2.5.3"

func TestE2E(t *testing.T) {
	doc := NewDoc(t, "raft-inspector.md")

	// ── Document start ──────────────────────────────────────────────────

	doc.H1("raft-inspector demo")
	doc.Text("This document demonstrates all `raft-inspector` commands against a 3-node OpenBao raft cluster. " +
		"All commands shown below can be reproduced by running them in order.")

	// ── Cluster setup ───────────────────────────────────────────────────

	doc.H2("Cluster setup")
	doc.Text("Start a 3-node OpenBao cluster using Docker containers with host networking. " +
		"The raft data directories are volume-mounted so that `raft-inspector` can access them from the host.")

	doc.H3("Create data directories and configs")
	doc.Run("mkdir -p testdata/node0 testdata/node1 testdata/node2")

	doc.Run(`cat <<'EOF' > testdata/node0.hcl
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
EOF`,
		`cat <<'EOF' > testdata/node1.hcl
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
EOF`,
		`cat <<'EOF' > testdata/node2.hcl
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
EOF`)

	doc.H3("Start the nodes")
	doc.Text("Start all three nodes as background containers. The `--user` flag ensures files are owned by the current user. " +
		"The `testdata` directory is mounted as `/host` inside the container.")

	doc.Run(
		"docker run -d --name bao-node0 \\\n    --network host \\\n    --user $(id -u):$(id -g) \\\n    -v $PWD/testdata:/host \\\n    "+image+" server -config=/host/node0.hcl > /dev/null",
		"docker run -d --name bao-node1 \\\n    --network host \\\n    --user $(id -u):$(id -g) \\\n    -v $PWD/testdata:/host \\\n    "+image+" server -config=/host/node1.hcl > /dev/null",
		"docker run -d --name bao-node2 \\\n    --network host \\\n    --user $(id -u):$(id -g) \\\n    -v $PWD/testdata:/host \\\n    "+image+" server -config=/host/node2.hcl > /dev/null",
	)

	doc.Text("Wait for node0 to be ready.")
	waitHTTP(t, "http://127.0.0.1:8200/v1/sys/health", 30)

	doc.H3("Initialize and unseal")
	doc.Text("Initialize the cluster on node0 with a single unseal key (for simplicity).")

	doc.Run("docker exec -e BAO_ADDR=http://127.0.0.1:8200 bao-node0 \\\n    bao operator init -key-shares=1 -key-threshold=1 -format=json \\\n    > testdata/init.json")

	doc.Text("Unseal node0 — it becomes the raft leader.")
	doc.Run("docker exec -e BAO_ADDR=http://127.0.0.1:8200 bao-node0 \\\n    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json) > /dev/null")

	doc.Text("Join node1 and node2 to the cluster, then unseal them.")
	doc.Run(
		"docker exec -e BAO_ADDR=http://127.0.0.1:8202 bao-node1 \\\n    bao operator raft join http://127.0.0.1:8200 > /dev/null",
		"docker exec -e BAO_ADDR=http://127.0.0.1:8204 bao-node2 \\\n    bao operator raft join http://127.0.0.1:8200 > /dev/null",
	)

	doc.Text("Unseal node1 and node2.")
	doc.Run(
		"docker exec -e BAO_ADDR=http://127.0.0.1:8202 bao-node1 \\\n    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json) > /dev/null",
		"docker exec -e BAO_ADDR=http://127.0.0.1:8204 bao-node2 \\\n    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json) > /dev/null",
	)

	waitHTTP(t, "http://127.0.0.1:8202/v1/sys/health", 15)
	waitHTTP(t, "http://127.0.0.1:8204/v1/sys/health", 15)

	doc.Text("Verify the cluster peers.")
	out := doc.Run("docker exec \\\n    -e BAO_ADDR=http://127.0.0.1:8200 \\\n    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \\\n    bao-node0 bao operator raft list-peers")
	containsAll(t, out, "node0", "node1", "node2")

	// ── Create test data ────────────────────────────────────────────────

	bao := "docker exec \\\n    -e BAO_ADDR=http://127.0.0.1:8200 \\\n    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \\\n    bao-node0 bao"

	doc.H2("Create test data")
	doc.Text("Enable a PKI secrets engine and generate a self-signed root CA.")

	doc.Run(
		bao+" secrets enable pki",
		bao+" secrets tune -max-lease-ttl=87600h pki",
		bao+" write -field=certificate pki/root/generate/internal \\\n    common_name='Test Root CA' ttl=87600h > /dev/null",
	)

	doc.Text("Enable a KV v2 secrets engine and write some secrets. Then update and delete entries to generate varied raft log operations.")

	doc.Run(
		bao+" secrets enable -path=secret kv-v2",
		bao+" kv put secret/myapp/config \\\n    endpoint=https://api.example.com api_key=secret > /dev/null",
		bao+" kv put secret/myapp/config \\\n    endpoint=https://api.example.com api_key=updated > /dev/null",
		bao+" kv put secret/myapp/credentials \\\n    username=admin password=mypassword > /dev/null",
		bao+" kv delete secret/myapp/credentials",
	)

	doc.Text("Write secrets in bulk, then disable the engine to delete all data at once. This simulates churn and produces free pages visible in the status output.")
	doc.Run(
		bao+" secrets enable -path=tmp kv-v2",
		"for i in $(seq 1 5); do "+bao+" kv put tmp/$i value=$(head -c 16384 /dev/urandom | base64 -w0); done > /dev/null",
		bao+" secrets disable tmp",
	)

	// ── raft-inspector commands ─────────────────────────────────────────

	doc.H2("raft-inspector status")
	doc.Text("Combined health overview reading both `raft/raft.db` and `vault.db`. " +
		"Note the Space Efficiency metric showing how much of the file is live data, and the estimated size after snapshot restore.")
	out = doc.Run("./raft-inspector -d testdata/node0 status")
	containsAll(t, out, "Current Term:", "Unapplied Entries:", "node0", "voter", "Space Efficiency:")

	doc.Text("Take a snapshot and restore it to reclaim space. " +
		"After restore, `vault.db` is rebuilt from scratch — its file size should match the estimate above. " +
		"The `raft.db` retains all log entries; they are only truncated by automatic snapshot compaction (once entry count exceeds `snapshot_threshold`).")
	doc.Run(
		bao+" operator raft snapshot save /host/backup.snap",
		bao+" operator raft snapshot restore -force /host/backup.snap",
	)
	waitHTTP(t, "http://127.0.0.1:8200/v1/sys/health", 15)
	doc.Run("docker exec -e BAO_ADDR=http://127.0.0.1:8200 bao-node0 \\\n    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json) > /dev/null")
	waitHTTP(t, "http://127.0.0.1:8200/v1/sys/health", 15)
	out = doc.Run("./raft-inspector -d testdata/node0 status 2>&1 \\\n    | grep -E '(─── BoltDB|File Size:|DB Logical Size:|Free Pages:|Space Efficiency:)'")
	containsAll(t, out, "BoltDB Stats")

	doc.H2("raft-inspector log")
	doc.Text("Show log entries with decrypted values. The `put` operations reveal the actual stored data.")
	out = doc.Run("./raft-inspector -d testdata/node0 log -n 3 \\\n    --unseal-key-file testdata/init.json")
	containsAll(t, out, "LogCommand")

	doc.H2("raft-inspector log --stats")
	doc.Text("Analyze log entry patterns: operation distribution and hot keys.")
	out = doc.Run("./raft-inspector -d testdata/node0 log --stats")
	containsAll(t, out, "Entry Count:", "put", "Hot Keys")

	doc.H2("raft-inspector fsm")
	doc.Text("Show total key count, top-level key path segments, and largest keys in the FSM data store (`vault.db`).")
	out = doc.Run("./raft-inspector -d testdata/node0 fsm")
	containsAll(t, out, "State Data", "Total Keys:", "core", "logical", "sys", "Largest Keys")

	doc.H2("raft-inspector fsm --prefix")
	doc.Text("List FSM keys matching a prefix. Shows encrypted value size after each key.")
	out = doc.Run("./raft-inspector -d testdata/node0 fsm --prefix sys/policy/")
	containsAll(t, out, "sys/policy/default", "kB")

	doc.Text("Show decrypted values for keys matching a prefix.")
	out = doc.Run("./raft-inspector -d testdata/node0 fsm --prefix sys/policy/ \\\n    --unseal-key-file testdata/init.json")
	containsAll(t, out, "sys/policy/default")

	doc.H2("raft-inspector snapshot")
	doc.Text("Inspect the snapshot archive. Default mode shows metadata, checksum verification, top-level key segments, and largest keys — same as `fsm`.")
	out = doc.Run("./raft-inspector snapshot testdata/backup.snap")
	containsAll(t, out, "Snapshot Metadata", "Index:", "Term:", "State Data", "Total Keys:", "Top-level Key Segments", "Largest Keys")

	doc.H2("raft-inspector snapshot --prefix")
	doc.Text("List snapshot keys matching a prefix.")
	out = doc.Run("./raft-inspector snapshot testdata/backup.snap --prefix sys/policy/")
	containsAll(t, out, "sys/policy/default", "kB")

	doc.Text("Decrypt values in the snapshot for keys matching a prefix.")
	out = doc.Run("./raft-inspector snapshot testdata/backup.snap \\\n    --prefix sys/policy/ --unseal-key-file testdata/init.json")
	containsAll(t, out, "sys/policy/default")
}

func containsAll(t *testing.T, got string, substrs ...string) {
	t.Helper()
	for _, s := range substrs {
		require.Contains(t, got, s)
	}
}

func waitHTTP(t *testing.T, url string, maxSecs int) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	for i := 0; i < maxSecs; i++ {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("timed out waiting for %s", url)
}
