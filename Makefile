.PHONY: build generate e2e clean

OPENBAO_SRC ?= $(HOME)/work/openbao

build:
	go build -o raft-inspector .

generate:
	protoc --go_out=. --go_opt=Mphysical/raft/types.proto=./ \
		-I $(OPENBAO_SRC) \
		physical/raft/types.proto
	sed -i 's/^package .*$$/package main/' types.pb.go

clean:
	docker rm -f bao-node0 bao-node1 bao-node2 2>/dev/null || true
	rm -rf testdata/node0 testdata/node1 testdata/node2 testdata/init.json testdata/backup.snap testdata/node0.hcl testdata/node1.hcl testdata/node2.hcl

# Run e2e tests: spins up OpenBao cluster, runs raft-inspector, generates raft-inspector.md.
# Testdata is preserved after the run for further experimentation.
e2e: clean build
	go test -v -tags e2e -run TestE2E -timeout 180s
