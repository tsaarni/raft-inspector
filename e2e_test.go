//go:build e2e

package main

import (
	"net/http"
	"testing"
	"time"
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
EOF`)

	doc.Run(`cat <<'EOF' > testdata/node1.hcl
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
EOF`)

	doc.Run(`cat <<'EOF' > testdata/node2.hcl
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

	doc.Run("docker run -d --name bao-node0 \\\n    --network host \\\n    --user $(id -u):$(id -g) \\\n    -v $PWD/testdata:/host \\\n    " + image + " server -config=/host/node0.hcl")
	doc.Run("docker run -d --name bao-node1 \\\n    --network host \\\n    --user $(id -u):$(id -g) \\\n    -v $PWD/testdata:/host \\\n    " + image + " server -config=/host/node1.hcl")
	doc.Run("docker run -d --name bao-node2 \\\n    --network host \\\n    --user $(id -u):$(id -g) \\\n    -v $PWD/testdata:/host \\\n    " + image + " server -config=/host/node2.hcl")

	doc.Text("Wait for node0 to be ready.")
	waitHTTP(t, "http://127.0.0.1:8200/v1/sys/health", 30)

	doc.H3("Initialize and unseal")
	doc.Text("Initialize the cluster on node0 with a single unseal key (for simplicity).")

	doc.RunFile(
		"docker exec -e BAO_ADDR=http://127.0.0.1:8200 bao-node0 \\\n    bao operator init -key-shares=1 -key-threshold=1 -format=json",
		"testdata/init.json")

	doc.Text("Unseal node0 — it becomes the raft leader.")
	doc.Run("docker exec -e BAO_ADDR=http://127.0.0.1:8200 bao-node0 \\\n    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json) > /dev/null")

	doc.Text("Join node1 and node2 to the cluster, then unseal them.")
	doc.Run("docker exec -e BAO_ADDR=http://127.0.0.1:8202 bao-node1 \\\n    bao operator raft join http://127.0.0.1:8200 > /dev/null")
	doc.Run("docker exec -e BAO_ADDR=http://127.0.0.1:8204 bao-node2 \\\n    bao operator raft join http://127.0.0.1:8200 > /dev/null")

	doc.Text("Unseal node1 and node2.")
	doc.Run("docker exec -e BAO_ADDR=http://127.0.0.1:8202 bao-node1 \\\n    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json) > /dev/null")
	doc.Run("docker exec -e BAO_ADDR=http://127.0.0.1:8204 bao-node2 \\\n    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json) > /dev/null")

	waitHTTP(t, "http://127.0.0.1:8202/v1/sys/health", 15)
	waitHTTP(t, "http://127.0.0.1:8204/v1/sys/health", 15)

	doc.Text("Verify the cluster peers.")
	doc.RunMatch("docker exec \\\n    -e BAO_ADDR=http://127.0.0.1:8200 \\\n    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \\\n    bao-node0 bao operator raft list-peers", []string{
		`node0`,
		`node1`,
		`node2`,
	})

	// ── Create test data ────────────────────────────────────────────────

	bao := "docker exec \\\n    -e BAO_ADDR=http://127.0.0.1:8200 \\\n    -e BAO_TOKEN=$(jq -r '.root_token' testdata/init.json) \\\n    bao-node0 bao"

	doc.H2("Create test data")
	doc.Text("Enable a PKI secrets engine and generate a self-signed root CA.")

	doc.Run(bao + " secrets enable pki")
	doc.Run(bao + " secrets tune -max-lease-ttl=87600h pki")
	doc.Run(bao + " write -field=certificate pki/root/generate/internal \\\n    common_name='Test Root CA' ttl=87600h > /dev/null")

	doc.Text("Enable a KV v2 secrets engine and write some secrets. Then update and delete entries to generate varied raft log operations.")

	doc.Run(bao + " secrets enable -path=secret kv-v2")
	doc.Run(bao + " kv put secret/myapp/config \\\n    endpoint=https://api.example.com api_key=secret > /dev/null")
	doc.Run(bao + " kv put secret/myapp/config \\\n    endpoint=https://api.example.com api_key=updated > /dev/null")
	doc.Run(bao + " kv put secret/myapp/credentials \\\n    username=admin password=mypassword > /dev/null")
	doc.Run(bao + " kv delete secret/myapp/credentials")

	doc.Text("Write secrets in bulk, then disable the engine to delete all data at once. This simulates churn and produces free pages visible in the status output.")
	doc.Run(bao + " secrets enable -path=tmp kv-v2")
	doc.Run("for i in $(seq 1 5); do " + bao + " kv put tmp/$i value=$(head -c 16384 /dev/urandom | base64 -w0); done > /dev/null")
	doc.Run(bao + " secrets disable tmp")

	// ── raft-inspector commands ─────────────────────────────────────────

	doc.H2("raft-inspector status")
	doc.Text("Combined health overview reading both `raft/raft.db` and `vault.db`. " +
		"Note the Space Efficiency metric showing how much of the file is live data, and the estimated size after snapshot restore.")
	doc.RunMatch("./raft-inspector -d testdata/node0 status", []string{
		`Current Term:`,
		`Unapplied Entries:\s+0`,
		`node0.*voter`,
		`Space Efficiency:`,
	})

	doc.Text("Take a snapshot and restore it to reclaim space. " +
		"After restore, `vault.db` is rebuilt from scratch — its file size should match the estimate above. " +
		"The `raft.db` retains all log entries; they are only truncated by automatic snapshot compaction (once entry count exceeds `snapshot_threshold`).")
	doc.Run(bao + " operator raft snapshot save /host/backup.snap")
	doc.Run(bao + " operator raft snapshot restore -force /host/backup.snap")
	waitHTTP(t, "http://127.0.0.1:8200/v1/sys/health", 15)
	doc.Run("docker exec -e BAO_ADDR=http://127.0.0.1:8200 bao-node0 \\\n    bao operator unseal $(jq -r '.unseal_keys_b64[0]' testdata/init.json) > /dev/null")
	waitHTTP(t, "http://127.0.0.1:8200/v1/sys/health", 15)
	doc.RunMatch("./raft-inspector -d testdata/node0 status 2>&1 \\\n    | grep -E '(─── BoltDB|File Size:|DB Logical Size:|Free Pages:|Space Efficiency:)'", []string{
		`BoltDB Stats`,
	})

	doc.H2("raft-inspector log")
	doc.Text("Show log entries with decrypted values. The `put` operations reveal the actual stored data.")
	doc.RunMatch("./raft-inspector -d testdata/node0 log -n 3 \\\n    --decrypt --unseal-key-file testdata/init.json", []string{
		`LogCommand`,
	})

	doc.H2("raft-inspector log --stats")
	doc.Text("Analyze log entry patterns: operation distribution and hot keys.")
	doc.RunMatch("./raft-inspector -d testdata/node0 log --stats", []string{
		`Entry Count:`,
		`put`,
		`Hot Keys`,
	})

	doc.H2("raft-inspector fsm")
	doc.Text("Show total key count in the FSM data store (`vault.db`).")
	doc.RunMatch("./raft-inspector -d testdata/node0 fsm", []string{
		`Total keys in data bucket:`,
	})

	doc.H2("raft-inspector fsm --top")
	doc.Text("Show top-level key path segments with counts.")
	doc.RunMatch("./raft-inspector -d testdata/node0 fsm --top", []string{
		`core`,
		`logical`,
		`sys`,
	})

	doc.H2("raft-inspector fsm --prefix")
	doc.Text("List FSM keys matching a prefix.")
	doc.RunMatch("./raft-inspector -d testdata/node0 fsm --prefix sys/policy/", []string{
		`sys/policy/default`,
	})

	doc.Text("Show decrypted values for keys matching a prefix.")
	doc.RunMatch("./raft-inspector -d testdata/node0 fsm --prefix sys/policy/ \\\n    --decrypt --unseal-key-file testdata/init.json", []string{
		`sys/policy/default`,
	})

	doc.H2("raft-inspector snapshot")
	doc.Text("Inspect the snapshot archive metadata.")
	doc.RunMatch("./raft-inspector -d testdata/node0 snapshot testdata/backup.snap", []string{
		`Index:`,
		`Term:`,
		`Total Keys:`,
	})

	doc.H2("raft-inspector snapshot --keys")
	doc.Text("List all key paths stored in the snapshot.")
	doc.RunMatch("./raft-inspector -d testdata/node0 snapshot testdata/backup.snap --keys", []string{
		`core/keyring`,
		`sys/policy/default`,
	})

	doc.Text("Decrypt values in the snapshot.")
	doc.RunMatch("./raft-inspector -d testdata/node0 snapshot testdata/backup.snap \\\n    --keys --decrypt --unseal-key-file testdata/init.json --limit 5", []string{
		`core/`,
	})
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
