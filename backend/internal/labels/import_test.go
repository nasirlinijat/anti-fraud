package labels_test

import (
	"strings"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
)

func TestImportCSVRejectsBadInputBeforeTheDatabase(t *testing.T) {
	cases := []struct {
		name, run, source, csv, want string
	}{
		{"missing run", "", "ieee-cis", "transaction_id,is_fraud\n", "run and source (ieee-cis or simulator) are required"},
		{"unknown source", "r1", "kaggle", "transaction_id,is_fraud\n", "run and source (ieee-cis or simulator) are required"},
		{"empty file", "r1", "ieee-cis", "", "EOF"},
		{"duplicate column", "r1", "ieee-cis", "transaction_id,is_fraud,is_fraud\n", "duplicate CSV column"},
		{"unknown column", "r1", "ieee-cis", "transaction_id,is_fraud,amount\n", "unknown CSV column amount"},
		{"missing transaction_id", "r1", "ieee-cis", "is_fraud,scenario\n", "missing transaction_id column"},
		{"missing is_fraud", "r1", "simulator", "transaction_id,scenario\n", "missing is_fraud column"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, err := labels.ImportCSV(t.Context(), nil, strings.NewReader(c.csv), c.run, c.source)
			if err == nil || err.Error() != c.want || n != 0 {
				t.Fatalf("got %d, %v; want error %q", n, err, c.want)
			}
		})
	}
}
