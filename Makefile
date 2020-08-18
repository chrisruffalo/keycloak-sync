# information
MAINTAINER=Chris Ruffalo
WEBSITE=https://github.com/chrisruffalo/keycloak-sync
DESCRIPTION=keycloak-sync is a command line tool for syncing Keycloak groups to OpenShift

# get $(SED) command
SED=$(shell which gsed || which sed)

# set relative paths
MKFILE_DIR:=$(abspath $(dir $(realpath $(firstword $(MAKEFILE_LIST)))))

# local arch (changed to standard names for build for debian/travis)
LOCALARCH=$(shell uname -m | $(SED) 's/x86_64/amd64/' | $(SED) -r 's/i?686/386/' | $(SED) 's/i386/386/' )

# enable passthrough of architecture flags to go
GOOS?=$(shell echo $(shell uname) | tr A-Z a-z)
GOARCH?=$(LOCALARCH)

# go commands and paths
GOPATH?=$(HOME)/go
GOBIN?=$(GOPATH)/bin/
GOCMD?=go
GODOWN=$(GOCMD) mod download
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get

# the build targets
BUILD_DIR=$(MKFILE_DIR)/build
BINARY_NAME=keycloak-sync

# build output
FINAL_NAME?=$(BINARY_NAME)-$(GOOS)-$(GOARCH)$(GOARM)

# get version and hash from git if not pas$(SED) in
VERSION?=$(shell git describe --tags $$(git rev-list --tags --max-count=1) | $(SED) -r -e 's/([^0-9.-]*)?-?v?([0-9.]*)-?([^-]*)?-?([^-]*)?/v\2/')
LONGVERSION?=$(shell git describe --tags | $(SED) 's/^$$/$(VERSION)/')
GITHASH?=$(shell git rev-parse HEAD | head -c7)
NUMBER?=$(shell echo $(LONGVERSION) | $(SED) -r -e 's/([^0-9.-]*)?-?v?([0-9.]*)-?([^-]*)?-?([^-]*)?/\2/' )
RELEASE?=$(shell echo $(LONGVERSION) | $(SED) -r -e 's/([^0-9.-]*)?-?v?([0-9.]*)-?([^-]*)?-?([^-]*)?/\3/' | $(SED) 's/^$$/1/' )
DESCRIPTOR?=$(shell echo $(LONGVERSION) | $(SED) -r -e 's/([^0-9.-]*)?-?v?([0-9.]*)-?([^-]*)?-?([^-]*)?/\1/' | $(SED) 's/^v$$//' | $(SED) 's/^\s$$//' )

# build targets for dockerized commands (build deb, build rpm)
OS_TYPE?=$(GOOS)
OS_VERSION?=7
OS_BIN_ARCH?=amd64
OS_ARCH?=x86_64s
BINARY_TARGET?=$(BINARY_NAME)-$(OS_TYPE)-$(OS_BIN_ARCH)

# set static flag
STATIC_FLAG?=-extldflags "-static"
ifeq ("$(GOOS)", "darwin")
  STATIC_FLAG=""
endif

# build tags can change by target platform, only linux builds for now though
GO_BUILD_TAGS?=$(GOOS)
GO_LD_FLAGS?=-s -w $(STATIC_FLAG) -X "github.com/chrisruffalo/keycloak-sync/version.Version=$(VERSION)" -X "github.com/chrisruffalo/keycloak-sync/version.GitHash=$(GITHASH)" -X "github.com/chrisruffalo/keycloak-sync/version.Release=$(RELEASE)" -X "github.com/chrisruffalo/keycloak-sync/version.Descriptor=$(DESCRIPTOR)"

all: test build
.PHONY: all announce prepare test build clean minimize package rpm deb docker tar npm webpack bench hash

announce: ## Debugging versions mainly for build and travis-ci
		@echo "$(BINARY_NAME)"
		@echo "=============================="
		@echo "longversion = $(LONGVERSION)"
		@echo "version = $(VERSION)"
		@echo "number = $(NUMBER)"
		@echo "release = $(RELEASE)"
		@echo "hash = $(GITHASH)"
		@echo "descriptor = $(DESCRIPTOR)"
		@echo "=============================="

build: announce  ## Build Binary
		$(GODOWN)
		# create build output dir
		mkdir -p $(BUILD_DIR)
		# create embeded resources
		$(GOBUILD) --tags "$(GO_BUILD_TAGS)" -ldflags "$(GO_LD_FLAGS)" -o "$(BUILD_DIR)/$(FINAL_NAME)" cmd/keycloak-sync.go

test: ## Do Unit Tests
		$(GODOWN)
		$(GOTEST) -v ./...

clean: ## Remove build artifacts
		# do go clean steps
		$(GOCLEAN)
		# remove build dir
		rm -rf $(BUILD_DIR)

