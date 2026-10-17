#!/bin/sh
set -e

apk add --no-cache protobuf
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
export PATH=$PATH:/go/bin

SERVICES="checkoutservice frontend inventoryservice productcatalogservice shippingservice"

for svc in $SERVICES; do
    echo "==> Generating protobuf for $svc..."
    protoc --proto_path=/apps/protos \
           --go_out=/apps/src/$svc/genproto --go_opt=paths=source_relative \
           --go-grpc_out=/apps/src/$svc/genproto --go-grpc_opt=paths=source_relative \
           /apps/protos/demo.proto
done

echo "Protoc generation successfully completed for all Go microservices!"
