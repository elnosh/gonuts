#!/bin/bash

annotationsFile=${1//.proto/.yaml}

protoc -I . \
  --grpc-gateway_out . \
  --grpc-gateway_opt logtostderr=true \
  --grpc-gateway_opt paths=source_relative \
  --grpc-gateway_opt allow_delete_body=true \
  "$1"

protoc  -I . \
  --openapiv2_out . \
  --openapiv2_opt logtostderr=true \
  --openapiv2_opt json_names_for_fields=false \
  --openapiv2_opt allow_delete_body=true \
  "$1"
