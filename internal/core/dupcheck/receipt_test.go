package dupcheck

import "testing"

func TestCompareDoesNotDecideOrSelfMatch(t *testing.T) {
	current := Invoice{SourceKey: "r1:f1", Number: " ab-12 ", Seller: "Shop", Type: "invoice"}
	got := Compare(current, []Invoice{
		{RecordID: "self", SourceKey: "r1:f1", Number: "AB-12", Seller: "Shop", Type: "invoice"},
		{RecordID: "prior", SourceKey: "r0:f1", Number: "AB-12", Seller: "shop", Type: "invoice"},
		{RecordID: "other", SourceKey: "r2:f1", Number: "AB-12", Seller: "Another", Type: "invoice"},
	})
	if len(got) != 2 || got[0].Code != "same_seller_number_type" || got[1].Code != "same_number_needs_review" {
		t.Fatalf("unexpected findings: %#v", got)
	}
}
