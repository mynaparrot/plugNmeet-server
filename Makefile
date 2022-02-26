NAME=plugnmeet-server
BINDIR=bin
FILE_PATH=cmd/server/*.go
GOBUILD=CGO_ENABLED=0 go build -ldflags '-w -s -buildid='
# The -w and -s flags reduce binary sizes by excluding unnecessary symbols and debug info
# The -buildid= flag makes builds reproducible

linux-amd64:
	GOARCH=amd64 GOOS=linux $(GOBUILD) -o $(BINDIR)/$(NAME)-$@ $(FILE_PATH)

linux-arm64:
	GOARCH=arm64 GOOS=linux $(GOBUILD) -o $(BINDIR)/$(NAME)-$@ $(FILE_PATH)

macos-amd64:
	GOARCH=amd64 GOOS=darwin $(GOBUILD) -o $(BINDIR)/$(NAME)-$@ $(FILE_PATH)

macos-arm64:
	GOARCH=arm64 GOOS=darwin $(GOBUILD) -o $(BINDIR)/$(NAME)-$@ $(FILE_PATH)

win64:
	GOARCH=amd64 GOOS=windows $(GOBUILD) -o $(BINDIR)/$(NAME)-$@.exe $(FILE_PATH)

releases: linux-amd64 linux-arm64 macos-amd64 macos-arm64 win64
	chmod +x $(BINDIR)/$(NAME)-*
	zip -m -j $(BINDIR)/$(NAME)-linux-amd64.zip $(BINDIR)/$(NAME)-linux-amd64
	zip -m -j $(BINDIR)/$(NAME)-linux-arm64.zip $(BINDIR)/$(NAME)-linux-arm64
	zip -m -j $(BINDIR)/$(NAME)-macos-amd64.zip $(BINDIR)/$(NAME)-macos-amd64
	zip -m -j $(BINDIR)/$(NAME)-macos-arm64.zip $(BINDIR)/$(NAME)-macos-arm64
	zip -m -j $(BINDIR)/$(NAME)-win64.zip $(BINDIR)/$(NAME)-win64.exe

clean:
	rm $(BINDIR)/*

# Remove trailing {} from the release upload url
GITHUB_UPLOAD_URL=$(shell echo $${GITHUB_RELEASE_UPLOAD_URL%\{*})

upload: releases
	curl -H "Authorization: token $(GITHUB_TOKEN)" -H "Content-Type: application/zip" --data-binary @$(BINDIR)/$(NAME)-linux-amd64.zip  "$(GITHUB_UPLOAD_URL)?name=$(NAME)-linux-amd64.zip"
	curl -H "Authorization: token $(GITHUB_TOKEN)" -H "Content-Type: application/zip" --data-binary @$(BINDIR)/$(NAME)-linux-arm64.zip  "$(GITHUB_UPLOAD_URL)?name=$(NAME)-linux-arm64.zip"
	curl -H "Authorization: token $(GITHUB_TOKEN)" -H "Content-Type: application/zip" --data-binary @$(BINDIR)/$(NAME)-macos-amd64.zip  "$(GITHUB_UPLOAD_URL)?name=$(NAME)-macos-amd64.zip"
	curl -H "Authorization: token $(GITHUB_TOKEN)" -H "Content-Type: application/zip" --data-binary @$(BINDIR)/$(NAME)-macos-arm64.zip  "$(GITHUB_UPLOAD_URL)?name=$(NAME)-macos-arm64.zip"
	curl -H "Authorization: token $(GITHUB_TOKEN)" -H "Content-Type: application/zip" --data-binary @$(BINDIR)/$(NAME)-win64.zip "$(GITHUB_UPLOAD_URL)?name=$(NAME)-win64.zip"
