PACKAGE_NAME          := shopware-cli
GOLANG_CROSS_VERSION  ?= v1.27.0
GORELEASER_ARGS       ?=

.PHONY: release-dry-run
release-dry-run:
	@docker run \
		--rm \
		-e CGO_ENABLED=1 \
		-v `pwd`:/go/src/$(PACKAGE_NAME) \
		-w /go/src/$(PACKAGE_NAME) \
		ghcr.io/shyim/goreleaser-cross:${GOLANG_CROSS_VERSION} \
		--clean --skip=validate --skip=publish --snapshot

.PHONY: release
release:
	docker run \
		--rm \
		-e CGO_ENABLED=1 \
		-e GITHUB_TOKEN \
		-e HOMEBREW_TAP_GITHUB_TOKEN \
		-v `pwd`:/go/src/$(PACKAGE_NAME) \
		-w /go/src/$(PACKAGE_NAME) \
		ghcr.io/shyim/goreleaser-cross:${GOLANG_CROSS_VERSION} \
		release --clean $(GORELEASER_ARGS)
