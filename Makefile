audit:
	go vet ./...
	staticcheck -checks=all ./...
	govulncheck ./...

.PHONY: audit
