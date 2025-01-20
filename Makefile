NAME=plugnmeet-server
BINDIR=bin
FILE_PATH=main.go
# The -w and -s flags reduce binary sizes by excluding unnecessary symbols and debug info
# The -buildid= flag makes builds reproducible
GOBUILD=CGO_ENABLED=0 go build -trimpath -ldflags '-w -s -buildid='

linux-amd64:
	GOARCH=amd64 GOOS=linux $(GOBUILD) -o $(BINDIR)/$(NAME)-$@ $(FILE_PATH)

linux-arm64:
	GOARCH=arm64 GOOS=linux $(GOBUILD) -o $(BINDIR)/$(NAME)-$@ $(FILE_PATH)

releases: linux-amd64 linux-arm64
	chmod +x $(BINDIR)/$(NAME)-*
	cp config_sample.yaml $(BINDIR)/
	zip -m -j $(BINDIR)/$(NAME)-linux-amd64.zip $(BINDIR)/$(NAME)-linux-amd64 $(BINDIR)/config_sample.yaml
	cp config_sample.yaml $(BINDIR)/
	zip -m -j $(BINDIR)/$(NAME)-linux-arm64.zip $(BINDIR)/$(NAME)-linux-arm64 $(BINDIR)/config_sample.yaml

clean:
	rm $(BINDIR)/*

# Remove trailing {} from the release upload url
GITHUB_UPLOAD_URL=$(shell echo $${GITHUB_RELEASE_UPLOAD_URL%\{*})

upload: releases
	curl -H "Authorization: token $(GITHUB_TOKEN)" -H "Content-Type: application/zip" --data-binary @$(BINDIR)/$(NAME)-linux-amd64.zip  "$(GITHUB_UPLOAD_URL)?name=$(NAME)-linux-amd64.zip"
	curl -H "Authorization: token $(GITHUB_TOKEN)" -H "Content-Type: application/zip" --data-binary @$(BINDIR)/$(NAME)-linux-arm64.zip  "$(GITHUB_UPLOAD_URL)?name=$(NAME)-linux-arm64.zip"
