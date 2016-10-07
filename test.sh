export LOG_LEVEL=debug

go test -v $(go list ./... | grep -v /vendor/ | grep -v /examples/)