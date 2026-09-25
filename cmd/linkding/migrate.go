package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/juev/linkding/internal/migratefrom"
)

func runMigration(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("migrate-from-linkding", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	engine := flags.String("source-engine", "sqlite", "upstream database engine: sqlite or postgres")
	sourceData := flags.String("source-data", "", "stopped upstream data directory")
	targetData := flags.String("target-data", "", "new Go data directory")
	sourceDSN := flags.String("source-dsn", "", "upstream PostgreSQL DSN")
	targetDSN := flags.String("target-dsn", "", "empty target PostgreSQL DSN")
	dryRun := flags.Bool("dry-run", false, "check the source without writing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *sourceData == "" || (!*dryRun && *targetData == "") {
		return fmt.Errorf("required: --source-data and, unless --dry-run, --target-data")
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	switch *engine {
	case "sqlite":
		if *sourceDSN != "" || *targetDSN != "" {
			return fmt.Errorf("PostgreSQL DSNs are only valid with --source-engine postgres")
		}
		if *dryRun {
			report, err := migratefrom.InspectSQLite(ctx, *sourceData)
			if err != nil {
				return err
			}
			return encoder.Encode(report)
		}
		report, err := migratefrom.MigrateSQLite(ctx, *sourceData, *targetData)
		if err != nil {
			return err
		}
		return encoder.Encode(report)
	case "postgres":
		if *sourceDSN == "" || (!*dryRun && *targetDSN == "") {
			return fmt.Errorf("PostgreSQL migration requires --source-dsn and --target-dsn")
		}
		if *dryRun {
			report, err := migratefrom.InspectPostgres(ctx, *sourceDSN, *sourceData)
			if err != nil {
				return err
			}
			return encoder.Encode(report)
		}
		report, err := migratefrom.MigratePostgres(ctx, *sourceDSN, *targetDSN, *sourceData, *targetData)
		if err != nil {
			return err
		}
		return encoder.Encode(report)
	default:
		return fmt.Errorf("unsupported source engine %q", *engine)
	}
}
