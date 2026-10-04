package bls

import (
	"strings"
	"testing"
)

func TestParseKeepsEmptyAsUnknown(t *testing.T) {
	in := "code,name,classification,kcal,protein,carbs,fat,fiber,sugar\n" +
		"C1,\"Hafer, roh\",C,343,11.38,,0,9.3,1.08\n"
	foods, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	f := foods[0]
	if f.Name != "Hafer, roh" || f.Classification != "C" {
		t.Errorf("got %+v", f)
	}
	if f.Carbs != nil {
		t.Errorf("empty carbs = %v, want nil (unknown)", *f.Carbs)
	}
	if f.Fat == nil || *f.Fat != 0 {
		t.Errorf("explicit 0 fat = %v, want 0", f.Fat)
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	const h = "code,name,classification,kcal,protein,carbs,fat,fiber,sugar\n"
	for name, in := range map[string]string{
		"header":    "code,name\nC1,x\n",
		"duplicate": h + "C1,a,C,1,,,,,\nC1,b,C,1,,,,,\n",
		"number":    h + "C1,a,C,abc,,,,,\n",
	} {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

// The embedded file is the real BLS 4.0 data.
func TestEmbeddedData(t *testing.T) {
	foods, err := Source{}.Foods()
	if err != nil {
		t.Fatal(err)
	}
	if len(foods) != 7140 {
		t.Errorf("got %d foods, want 7140", len(foods))
	}
	for _, f := range foods {
		if f.Kcal == nil {
			t.Fatalf("%s %q has no kcal", f.Code, f.Name)
		}
	}
}
