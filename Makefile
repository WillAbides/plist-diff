GOCMD=go
GOBUILD=$(GOCMD) build
PATH := "${CURDIR}/bin:$(PATH)"

.PHONY: gobuildcache

bin/golangci-lint:
	script/bindown install $(notdir $@)

bin/shellcheck:
	script/bindown install $(notdir $@)

bin/gofumpt:
	script/bindown install $(notdir $@)

bin/plist-diff: gobuildcache
	go build -o  $@ .

HANDCRAFTED_REV := 082e94edadf89c33db0afb48889c8419a2cb46a9
bin/handcrafted:
	GOBIN=${CURDIR}/bin \
	go install github.com/willabides/handcrafted@$(HANDCRAFTED_REV)
