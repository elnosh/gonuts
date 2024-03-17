#!/bin/bash

#generate and push
buf generate
buf generate --template buf.gen.tag.yaml

# generate grpc gateways
./gw.sh cashu.proto


#clean go mods
go mod tidy
