STEAMPIPE_INSTALL_DIR ?= ~/.steampipe
BUILD_TAGS = netgo
install:
	go build -o $(STEAMPIPE_INSTALL_DIR)/plugins/hub.steampipe.io/plugins/turbot/salesforce@latest/steampipe-plugin-salesforce.plugin -tags "${BUILD_TAGS}" *.go

test:
	go test ./salesforce/ -v -count=1

test-integration:
	go test -tags integration ./salesforce/ -v -count=1 -timeout 120s
