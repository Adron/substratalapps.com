// Package dataapi implements db.DB over the RDS Data API: IAM-signed HTTPS
// to Aurora Serverless v2, with no VPC attachment (see DEPLOYMENT.md → Why
// no VPC). It's what every Lambda runs in AWS.
package dataapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata/types"

	"github.com/Adron/substratalapps.com/internal/db"
)

// DB is a Data API client bound to one cluster, secret, and database.
type DB struct {
	client      *rdsdata.Client
	resourceARN string
	secretARN   string
	database    string
}

// New returns a Data API–backed db.DB.
func New(client *rdsdata.Client, resourceARN, secretARN, database string) *DB {
	return &DB{client: client, resourceARN: resourceARN, secretARN: secretARN, database: database}
}

// Close is a no-op; the Data API is connectionless.
func (d *DB) Close() {}

// Tx implements db.DB with BeginTransaction/CommitTransaction.
func (d *DB) Tx(ctx context.Context, s db.Settings, fn func(db.Querier) error) error {
	begin, err := d.client.BeginTransaction(ctx, &rdsdata.BeginTransactionInput{
		ResourceArn: &d.resourceARN, SecretArn: &d.secretARN, Database: &d.database,
	})
	if err != nil {
		return err
	}
	q := querier{d: d, txID: begin.TransactionId}
	rollback := func() {
		// Best effort: a failed rollback leaves the transaction to time out
		// server-side, which is the Data API's own documented behavior.
		_, _ = d.client.RollbackTransaction(context.WithoutCancel(ctx), &rdsdata.RollbackTransactionInput{
			ResourceArn: &d.resourceARN, SecretArn: &d.secretARN, TransactionId: begin.TransactionId,
		})
	}
	if _, err := q.Exec(ctx, db.SetupSQL, s.SetupArgs()); err != nil {
		rollback()
		return err
	}
	if err := fn(q); err != nil {
		rollback()
		return err
	}
	_, err = d.client.CommitTransaction(ctx, &rdsdata.CommitTransactionInput{
		ResourceArn: &d.resourceARN, SecretArn: &d.secretARN, TransactionId: begin.TransactionId,
	})
	return err
}

// ExecScript implements db.DB: every statement in one Data API transaction.
func (d *DB) ExecScript(ctx context.Context, statements []string) error {
	begin, err := d.client.BeginTransaction(ctx, &rdsdata.BeginTransactionInput{
		ResourceArn: &d.resourceARN, SecretArn: &d.secretARN, Database: &d.database,
	})
	if err != nil {
		return err
	}
	for _, st := range statements {
		st := st
		if _, err := d.client.ExecuteStatement(ctx, &rdsdata.ExecuteStatementInput{
			ResourceArn: &d.resourceARN, SecretArn: &d.secretARN, Database: &d.database,
			TransactionId: begin.TransactionId, Sql: &st,
		}); err != nil {
			_, _ = d.client.RollbackTransaction(context.WithoutCancel(ctx), &rdsdata.RollbackTransactionInput{
				ResourceArn: &d.resourceARN, SecretArn: &d.secretARN, TransactionId: begin.TransactionId,
			})
			return fmt.Errorf("%w\n--- statement ---\n%s", translate(err), st)
		}
	}
	_, err = d.client.CommitTransaction(ctx, &rdsdata.CommitTransactionInput{
		ResourceArn: &d.resourceARN, SecretArn: &d.secretARN, TransactionId: begin.TransactionId,
	})
	return err
}

// Autocommit implements db.DB: statements run without a transaction id.
func (d *DB) Autocommit() db.Querier { return querier{d: d} }

type querier struct {
	d    *DB
	txID *string
}

func (q querier) run(ctx context.Context, sql string, args db.Args) (*rdsdata.ExecuteStatementOutput, error) {
	params, err := parameters(sql, args)
	if err != nil {
		return nil, err
	}
	out, err := q.d.client.ExecuteStatement(ctx, &rdsdata.ExecuteStatementInput{
		ResourceArn:   &q.d.resourceARN,
		SecretArn:     &q.d.secretARN,
		Database:      &q.d.database,
		TransactionId: q.txID,
		Sql:           &sql,
		Parameters:    params,
	})
	return out, translate(err)
}

func (q querier) Query(ctx context.Context, sql string, args db.Args) ([]json.RawMessage, error) {
	out, err := q.run(ctx, sql, args)
	if err != nil {
		return nil, err
	}
	rows := make([]json.RawMessage, 0, len(out.Records))
	for _, rec := range out.Records {
		if len(rec) != 1 {
			return nil, fmt.Errorf("dataapi: query must select exactly one json column, got %d", len(rec))
		}
		switch f := rec[0].(type) {
		case *types.FieldMemberStringValue:
			rows = append(rows, json.RawMessage(f.Value))
		case *types.FieldMemberIsNull:
			rows = append(rows, json.RawMessage("null"))
		default:
			return nil, fmt.Errorf("dataapi: expected a json column, got %T", f)
		}
	}
	return rows, nil
}

func (q querier) Exec(ctx context.Context, sql string, args db.Args) (int64, error) {
	out, err := q.run(ctx, sql, args)
	if err != nil {
		return 0, err
	}
	return out.NumberOfRecordsUpdated, nil
}

var paramName = regexp.MustCompile(`(::?)([a-zA-Z_][a-zA-Z0-9_]*)`)

// parameters builds Data API SqlParameters for every :name the statement
// uses. The Data API binds :name natively, so the SQL text is unchanged.
func parameters(sql string, args db.Args) ([]types.SqlParameter, error) {
	seen := map[string]bool{}
	var params []types.SqlParameter
	for _, m := range paramName.FindAllStringSubmatch(sql, -1) {
		if m[1] == "::" || seen[m[2]] {
			continue
		}
		name := m[2]
		seen[name] = true
		v, ok := args[name]
		if !ok {
			return nil, fmt.Errorf("dataapi: missing parameter %q", name)
		}
		field, err := toField(db.Normalize(v))
		if err != nil {
			return nil, fmt.Errorf("dataapi: parameter %q: %w", name, err)
		}
		params = append(params, types.SqlParameter{Name: aws.String(name), Value: field})
	}
	return params, nil
}

func toField(v any) (types.Field, error) {
	switch t := v.(type) {
	case nil:
		return &types.FieldMemberIsNull{Value: true}, nil
	case string:
		return &types.FieldMemberStringValue{Value: t}, nil
	case bool:
		return &types.FieldMemberBooleanValue{Value: t}, nil
	case int64:
		return &types.FieldMemberLongValue{Value: t}, nil
	case float64:
		return &types.FieldMemberDoubleValue{Value: t}, nil
	default:
		return nil, fmt.Errorf("unsupported type %T (use db.Time/JSON/TextArray)", v)
	}
}

var sqlState = regexp.MustCompile(`SQLState: (\d{5})`)
var constraintName = regexp.MustCompile(`constraint "([^"]+)"`)

// translate maps a Data API database error to db.ConstraintError. The
// Data API reports Postgres errors as BadRequestException text, so the
// SQLSTATE and constraint name are parsed from the message.
func translate(err error) error {
	if err == nil {
		return nil
	}
	var bre *types.BadRequestException
	if !errors.As(err, &bre) || bre.Message == nil {
		return err
	}
	msg := *bre.Message
	m := sqlState.FindStringSubmatch(msg)
	if m == nil || !strings.HasPrefix(m[1], "23") {
		return err
	}
	ce := &db.ConstraintError{Code: m[1], Message: msg}
	if c := constraintName.FindStringSubmatch(msg); c != nil {
		ce.Constraint = c[1]
	}
	return ce
}
