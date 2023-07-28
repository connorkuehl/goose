#!/usr/bin/env bash

function cleanup() {
    docker compose down
}
trap cleanup EXIT

function wait_for_db_ready() {
    docker compose logs db | grep -q "database system is ready to accept connections"
    # shellcheck disable=SC2181
    while [ $? -ne 0 ]; do
        echo "Database isn't ready yet, sleeping..."
        sleep 1
        docker compose logs db | grep -q "database system is ready to accept connections"
    done
}

# docker-compose.yaml
export GOOSE_INTEGRATION_POSTGRES_DSN="postgres://goose:goose@localhost:5432/goose?sslmode=disable"

docker compose up --detach
wait_for_db_ready

go test -tags=integration -v -run=^TestIntegration ./...