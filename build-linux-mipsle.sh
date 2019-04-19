#!/usr/bin/env sh

GOOS=linux GOARCH=mipsle go build -mod=vendor -o nz-linux-mipsle .
