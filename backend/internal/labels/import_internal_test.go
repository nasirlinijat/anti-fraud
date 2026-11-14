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
