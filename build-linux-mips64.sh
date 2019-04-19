#!/usr/bin/env sh

GOOS=linux GOARCH=mips64 go build -mod=vendor -o nz-linux-mips64 .
