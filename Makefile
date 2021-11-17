.PHONY: prepare
prepare:
	go run -ldflags "-w -s -X main.version=`git describe --exact-match --tags $(git log -n1 --pretty='%h')`" tools/prepare.go

.PHONY: build
build:
	go build -ldflags "-w -s -X main.version=`git describe --exact-match --tags $(git log -n1 --pretty='%h')`" -o dapr-standalone cmd/dapr-standalone-installer/main.go

.PHONY: release-dry-run
release-dry-run:
	goreleaser --rm-dist --skip-validate --skip-publish --snapshot

.PHONY: release
release:
	@if [ ! -f ".release-env" ]; then \
		echo "\033[91m.release-env is required for release\033[0m";\
		exit 1;\
	fi
	goreleaser release --rm-dist
