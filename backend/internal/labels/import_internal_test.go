package labels

import (
	"encoding/csv"
	"strings"
	"testing"
)

func TestReadColumnsFindsEachColumnInAnyOrder(t *testing.T) {
	cases := []struct {
		header string
		want   columns
	}{
		{"transaction_id,is_fraud", columns{transactionID: 0, isFraud: 1, scenario: -1}},
		{"is_fraud,transaction_id", columns{transactionID: 1, isFraud: 0, scenario: -1}},
		{"scenario,is_fraud,transaction_id", columns{transactionID: 2, isFraud: 1, scenario: 0}},
	}
	for _, c := range cases {
		t.Run(c.header, func(t *testing.T) {
			got, err := readColumns(csv.NewReader(strings.NewReader(c.header + "\n")))
			if err != nil || got != c.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, c.want)
			}
		})
	}
}

func TestParseRecordAcceptsValidRows(t *testing.T) {
	withScenario := columns{transactionID: 0, isFraud: 1, scenario: 2}
	cases := []struct {
		name         string
		record       []string
		cols         columns
		source       string
		wantFraud    bool
		wantScenario string
	}{
		{"ieee-cis fraud", []string{"txn-1", "1"}, columns{transactionID: 0, isFraud: 1, scenario: -1}, "ieee-cis", true, ""},
		{"ieee-cis not fraud", []string{"txn-2", "0"}, columns{transactionID: 0, isFraud: 1, scenario: -1}, "ieee-cis", false, ""},
		{"ieee-cis with an empty scenario", []string{"txn-3", "0", ""}, withScenario, "ieee-cis", false, ""},
		{"simulator with a scenario", []string{"txn-4", "1", "card_testing"}, withScenario, "simulator", true, "card_testing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, err := parseRecord(c.record, c.cols, "run-1", c.source)
			if err != nil {
				t.Fatal(err)
			}
			scenario := ""
			if l.Scenario != nil {
				scenario = *l.Scenario
			}
			if l.TransactionID != c.record[0] || l.RunID != "run-1" || l.Source != c.source || l.Fraud != c.wantFraud || scenario != c.wantScenario {
				t.Fatalf("got %+v with scenario %q", l, scenario)
			}
		})
	}
}

func TestParseRecordRejectsEachBrokenRule(t *testing.T) {
	plain := columns{transactionID: 0, isFraud: 1, scenario: -1}
	withScenario := columns{transactionID: 0, isFraud: 1, scenario: 2}
	cases := []struct {
		name   string
		record []string
		cols   columns
		source string
		want   string
	}{
		{"is_fraud is a word", []string{"txn-1", "yes"}, plain, "ieee-cis", "is_fraud must be 0 or 1"},
		{"is_fraud is empty", []string{"txn-1", ""}, plain, "ieee-cis", "is_fraud must be 0 or 1"},
		{"is_fraud is 2", []string{"txn-1", "2"}, plain, "ieee-cis", "is_fraud must be 0 or 1"},
		{"empty transaction_id", []string{"", "1"}, plain, "ieee-cis", "transaction and run IDs are required"},
		{"ieee-cis with a scenario", []string{"txn-1", "1", "card_testing"}, withScenario, "ieee-cis", "invalid label source or scenario"},
		{"simulator without a scenario column", []string{"txn-1", "1"}, plain, "simulator", "invalid label source or scenario"},
		{"simulator with an empty scenario", []string{"txn-1", "1", ""}, withScenario, "simulator", "invalid label source or scenario"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, err := parseRecord(c.record, c.cols, "run-1", c.source)
			if err == nil || err.Error() != c.want || l.TransactionID != "" || l.Scenario != nil {
				t.Fatalf("got %+v, %v; want error %q", l, err, c.want)
			}
		})
	}
}

func TestLabelRowsStreamsRowsAndNamesTheFailingLine(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		rows    int
		wantErr string
	}{
		{"all rows valid", "txn-1,1\ntxn-2,0\n", 2, ""},
		{"rule broken on line 3", "txn-1,1\ntxn-2,yes\ntxn-3,0\n", 1, "line 3: is_fraud must be 0 or 1"},
		{"broken CSV on line 2", "txn-1,1,extra\n", 0, "line 2: record on line 2: wrong number of fields"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reader := csv.NewReader(strings.NewReader("transaction_id,is_fraud\n" + c.body))
			cols, err := readColumns(reader)
			if err != nil {
				t.Fatal(err)
			}
			rows := labelRows(reader, cols, "run-1", "ieee-cis")
			n := 0
			for rows.Next() {
				values, err := rows.Values()
				if err != nil || len(values) != 5 || values[1] != "run-1" {
					t.Fatalf("row %d: %v, %v", n, values, err)
				}
				n++
			}
			got := ""
			if rows.Err() != nil {
				got = rows.Err().Error()
			}
			if n != c.rows || got != c.wantErr {
				t.Fatalf("got %d rows and %q; want %d rows and %q", n, got, c.rows, c.wantErr)
			}
		})
	}
}

func TestReadColumnsLeavesTheRecordsUnread(t *testing.T) {
	reader := csv.NewReader(strings.NewReader("transaction_id,is_fraud\ntxn-1,1\n"))
	if _, err := readColumns(reader); err != nil {
		t.Fatal(err)
	}
	record, err := reader.Read()
	if err != nil || record[0] != "txn-1" {
		t.Fatalf("got %v, %v; want the first record", record, err)
	}
}
