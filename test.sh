export LOG_LEVEL=debug

rm -rf vendor/github.com/heroku/docker-registry-client/vendor
go test -v $(go list ./... | grep -v /vendor/ | grep -v /examples/)