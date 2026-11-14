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
	if err := checkImportArgs(run, source); err != nil {
		return 0, err
	}
	reader := csv.NewReader(input)
	cols, err := readColumns(reader)
	if err != nil {
		return 0, err
	}
	var n int64
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if err := stageLabels(ctx, tx, labelRows(reader, cols, run, source)); err != nil {
			return err
		}
		if err := requireDecisions(ctx, tx); err != nil {
			return err
		}
		var err error
		if n, err = insertLabels(ctx, tx); err != nil {
			return err
		}
		return rejectConflicts(ctx, tx)
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

func checkImportArgs(run, source string) error {
	if run == "" || (source != "ieee-cis" && source != "simulator") {
		return errors.New("run and source (ieee-cis or simulator) are required")
	}
	return nil
}

type columns struct {
	transactionID, isFraud, scenario int
}

func readColumns(reader *csv.Reader) (columns, error) {
	header, err := reader.Read()
	if err != nil {
		return columns{}, err
	}
	indexes := map[string]int{}
	for i, name := range header {
		if _, ok := indexes[name]; ok {
			return columns{}, errors.New("duplicate CSV column")
		}
		if name != "transaction_id" && name != "is_fraud" && name != "scenario" {
			return columns{}, fmt.Errorf("unknown CSV column %s", name)
		}
		indexes[name] = i
	}
	cols := columns{scenario: -1}
	var ok bool
	if cols.transactionID, ok = indexes["transaction_id"]; !ok {
		return columns{}, errors.New("missing transaction_id column")
	}
	if cols.isFraud, ok = indexes["is_fraud"]; !ok {
		return columns{}, errors.New("missing is_fraud column")
	}
	if i, ok := indexes["scenario"]; ok {
		cols.scenario = i
	}
	return cols, nil
}

func parseRecord(record []string, cols columns, run, source string) (Label, error) {
	flag := record[cols.isFraud]
	if flag != "0" && flag != "1" {
		return Label{}, errors.New("is_fraud must be 0 or 1")
	}
	var scenario *string
	if cols.scenario >= 0 && record[cols.scenario] != "" {
		v := record[cols.scenario]
		scenario = &v
	}
	l := Label{TransactionID: record[cols.transactionID], RunID: run, Source: source, Scenario: scenario, Fraud: flag == "1"}
	if err := l.Validate(); err != nil {
		return Label{}, err
	}
	return l, nil
}

func labelRows(reader *csv.Reader, cols columns, run, source string) pgx.CopyFromSource {
	line := 1
	return pgx.CopyFromFunc(func() ([]any, error) {
		record, err := reader.Read()
		if err == io.EOF {
			return nil, nil
		}
		line++
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		l, err := parseRecord(record, cols, run, source)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		return []any{l.TransactionID, l.RunID, l.Source, l.Scenario, l.Fraud}, nil
	})
}

func stageLabels(ctx context.Context, tx pgx.Tx, rows pgx.CopyFromSource) error {
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE label_import(transaction_id text PRIMARY KEY,run_id text,source text,scenario text,is_fraud boolean) ON COMMIT DROP`); err != nil {
		return err
	}
	_, err := tx.CopyFrom(ctx, pgx.Identifier{"label_import"}, []string{"transaction_id", "run_id", "source", "scenario", "is_fraud"}, rows)
	return err
}

func requireDecisions(ctx context.Context, tx pgx.Tx) error {
	var missing int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM label_import l WHERE NOT EXISTS(SELECT 1 FROM purchases p JOIN decisions d ON d.purchase_id=p.id WHERE p.transaction_id=l.transaction_id)`).Scan(&missing); err != nil {
		return err
	}
	if missing != 0 {
		return fmt.Errorf("%d labels have no saved decision", missing)
	}
	return nil
}

func insertLabels(ctx context.Context, tx pgx.Tx) (int64, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO purchase_labels(transaction_id,run_id,source,scenario,is_fraud) SELECT transaction_id,run_id,source,scenario,is_fraud FROM label_import ON CONFLICT(transaction_id) DO NOTHING`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func rejectConflicts(ctx context.Context, tx pgx.Tx) error {
	var conflicts int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM label_import i JOIN purchase_labels l USING(transaction_id) WHERE ROW(i.run_id,i.source,i.scenario,i.is_fraud) IS DISTINCT FROM ROW(l.run_id,l.source,l.scenario,l.is_fraud)`).Scan(&conflicts); err != nil {
		return err
	}
	if conflicts != 0 {
		return fmt.Errorf("%d conflicting labels; import rolled back", conflicts)
	}
	return nil
}
