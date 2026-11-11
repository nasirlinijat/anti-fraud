package labels

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
)

// ImportCSV validates and inserts a complete file atomically with bounded client memory.
func ImportCSV(ctx context.Context, pool *pgxpool.Pool, input io.Reader, run, source string) (int64, error) {
	if run == "" || (source != "ieee-cis" && source != "simulator") {
		return 0, errors.New("run and source (ieee-cis or simulator) are required")
	}
	reader := csv.NewReader(input)
	header, err := reader.Read()
	if err != nil {
		return 0, err
	}
	indexes := map[string]int{}
	for i, name := range header {
		if _, ok := indexes[name]; ok {
			return 0, errors.New("duplicate CSV column")
		}
		if name != "transaction_id" && name != "is_fraud" && name != "scenario" {
			return 0, fmt.Errorf("unknown CSV column %s", name)
		}
		indexes[name] = i
	}
	if _, ok := indexes["transaction_id"]; !ok {
		return 0, errors.New("missing transaction_id column")
	}
	if _, ok := indexes["is_fraud"]; !ok {
		return 0, errors.New("missing is_fraud column")
	}
	n := int64(0)
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE label_import(transaction_id text PRIMARY KEY,run_id text,source text,scenario text,is_fraud boolean) ON COMMIT DROP`); err != nil {
			return err
		}
		line := 1
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"label_import"}, []string{"transaction_id", "run_id", "source", "scenario", "is_fraud"}, pgx.CopyFromFunc(func() ([]any, error) {
			record, err := reader.Read()
			if err == io.EOF {
				return nil, nil
			}
			line++
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
			flag := record[indexes["is_fraud"]]
			if flag != "0" && flag != "1" {
				return nil, fmt.Errorf("line %d: is_fraud must be 0 or 1", line)
			}
			var scenario *string
			if i, ok := indexes["scenario"]; ok && record[i] != "" {
				v := record[i]
				scenario = &v
			}
			l := Label{record[indexes["transaction_id"]], run, source, scenario, flag == "1"}
			if err := l.Validate(); err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
			return []any{l.TransactionID, l.RunID, l.Source, l.Scenario, l.Fraud}, nil
		}))
		if err != nil {
			return err
		}
		var missing int64
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM label_import l WHERE NOT EXISTS(SELECT 1 FROM purchases p JOIN decisions d ON d.purchase_id=p.id WHERE p.transaction_id=l.transaction_id)`).Scan(&missing); err != nil {
			return err
		}
		if missing != 0 {
			return fmt.Errorf("%d labels have no saved decision", missing)
		}
		tag, err := tx.Exec(ctx, `INSERT INTO purchase_labels(transaction_id,run_id,source,scenario,is_fraud) SELECT transaction_id,run_id,source,scenario,is_fraud FROM label_import ON CONFLICT(transaction_id) DO NOTHING`)
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
		var conflicts int64
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM label_import i JOIN purchase_labels l USING(transaction_id) WHERE ROW(i.run_id,i.source,i.scenario,i.is_fraud) IS DISTINCT FROM ROW(l.run_id,l.source,l.scenario,l.is_fraud)`).Scan(&conflicts); err != nil {
			return err
		}
		if conflicts != 0 {
			return fmt.Errorf("%d conflicting labels; import rolled back", conflicts)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}
