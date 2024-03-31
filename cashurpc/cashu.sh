#!/bin/bash

annotationsFile=${1//.proto/.yaml}

for protoFile in ./*.proto
do
  protoc -I . \
    --cashu_out . \
    "${protoFile}"
done