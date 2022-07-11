NAME ?= terraform-provider-leaseweb
VERSION ?= 0.0.1
GOOS ?= darwin
GOARCH ?= arm64
BINARY = $(NAME)-$(VERSION)-$(GOOS)-$(GOARCH)

default: install

build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $(BINARY)

release:
	$(MAKE) build GOOS=darwin GOARCH=amd64
	$(MAKE) build GOOS=darwin GOARCH=amd64
	$(MAKE) build GOOS=freebsd GOARCH=386
	$(MAKE) build GOOS=freebsd GOARCH=amd64
	$(MAKE) build GOOS=freebsd GOARCH=arm
	$(MAKE) build GOOS=linux GOARCH=386
	$(MAKE) build GOOS=linux GOARCH=amd64
	$(MAKE) build GOOS=linux GOARCH=arm
	$(MAKE) build GOOS=openbsd GOARCH=386
	$(MAKE) build GOOS=openbsd GOARCH=amd64
	$(MAKE) build GOOS=solaris GOARCH=amd64
	$(MAKE) build GOOS=windows GOARCH=386
	$(MAKE) build GOOS=windows GOARCH=amd64

install: build
	mkdir -p ~/.terraform.d/plugins/git.ocom.com/infra/leaseweb/$(VERSION)/$(GOOS)_$(GOARCH)
	mv $(BINARY) ~/.terraform.d/plugins/git.ocom.com/infra/leaseweb/$(VERSION)/$(GOOS)_$(GOARCH)/$(NAME)
