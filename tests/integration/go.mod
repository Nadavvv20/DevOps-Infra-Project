module github.com/Nadavvv20/DevOps-Infra-Project/tests/integration

go 1.26.0

require (
	github.com/GoogleCloudPlatform/microservices-demo/src/checkoutservice v0.0.0-00010101000000-000000000000
	github.com/GoogleCloudPlatform/microservices-demo/src/inventoryservice v0.0.0-00010101000000-000000000000
	github.com/GoogleCloudPlatform/microservices-demo/src/shippingservice v0.0.0-00010101000000-000000000000
	github.com/alicebob/miniredis/v2 v2.34.0
	github.com/aws/aws-sdk-go-v2 v1.47.3
	github.com/aws/aws-sdk-go-v2/config v1.33.9
	github.com/aws/aws-sdk-go-v2/service/sns v1.47.5
	github.com/aws/aws-sdk-go-v2/service/sqs v1.52.4
	github.com/jackc/pgx/v5 v5.11.0
	github.com/redis/go-redis/v9 v9.23.0
)

require (
	github.com/alicebob/gopher-json v0.0.0-20230218143504-906a9b012302 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.20.9 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.20.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.6 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.6 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.10.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.38.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.43.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.51.4 // indirect
	github.com/aws/smithy-go v1.28.5 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/atomic v1.12.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)

replace (
	github.com/GoogleCloudPlatform/microservices-demo/src/checkoutservice => ../../../Google-Boutique-apps/src/checkoutservice
	github.com/GoogleCloudPlatform/microservices-demo/src/inventoryservice => ../../../Google-Boutique-apps/src/inventoryservice
	github.com/GoogleCloudPlatform/microservices-demo/src/shippingservice => ../../../Google-Boutique-apps/src/shippingservice
)
