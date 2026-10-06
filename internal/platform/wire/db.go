// Package wire builds the concrete dependencies (database, signer,
// encrypter, email, queue) from Config. It's the only place that knows
// which implementation each environment uses.
package wire

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata"

	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/db/dataapi"
	"github.com/Adron/substratalapps.com/internal/db/pg"
	"github.com/Adron/substratalapps.com/internal/platform/config"
)

// AWS loads the default AWS SDK config. AWS_ENDPOINT_URL (LocalStack) is
// honored by the SDK itself, so local and real AWS share this code path.
func AWS(ctx context.Context) (aws.Config, error) {
	return awsconfig.LoadDefaultConfig(ctx)
}

// Database opens the configured backend.
func Database(ctx context.Context, c config.Config) (db.DB, error) {
	switch c.DatabaseBackend {
	case "pg":
		return pg.Open(ctx, c.DatabaseURL)
	case "dataapi":
		if c.DBClusterARN == "" || c.DBSecretARN == "" {
			return nil, fmt.Errorf("wire: dataapi backend needs DB_CLUSTER_ARN and DB_SECRET_ARN")
		}
		awsCfg, err := AWS(ctx)
		if err != nil {
			return nil, err
		}
		return dataapi.New(rdsdata.NewFromConfig(awsCfg), c.DBClusterARN, c.DBSecretARN, c.DBName), nil
	default:
		return nil, fmt.Errorf("wire: unknown DATABASE_BACKEND %q", c.DatabaseBackend)
	}
}
