package catalog

import (
	"errors"
	"testing"
)

func TestParseRef(t *testing.T) {
	tests := []struct {
		in      string
		defKind Kind
		defNS   string
		want    string
		wantErr bool
	}{
		{"component:payments", "", "default", "component:default/payments", false},
		{"Component:Default/Payments", "", "default", "component:default/payments", false},
		{"team-a", KindGroup, "default", "group:default/team-a", false},
		{"ops/team-a", KindGroup, "default", "group:ops/team-a", false},
		{"team-a", KindGroup, "ops", "group:ops/team-a", false},
		{"api:x.v1_beta", "", "default", "api:default/x.v1_beta", false},
		{"resource:db", KindGroup, "default", "resource:default/db", false},
		{"payments", "", "default", "", true},          // kind required
		{"user:alice", KindGroup, "default", "", true}, // unsupported kind
		{"", KindGroup, "default", "", true},
		{"group:Bad_NS/x", "", "default", "", true},
		{"group:-bad", "", "default", "", true},
		{"group:a--b", "", "default", "", true},
		{"group:" + string(make([]byte, 64)), "", "default", "", true},
	}
	for _, tt := range tests {
		got, err := ParseRef(tt.in, tt.defKind, tt.defNS)
		if tt.wantErr {
			var re *RefError
			if !errors.As(err, &re) {
				t.Errorf("ParseRef(%q) err = %v, want *RefError", tt.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRef(%q) unexpected error %v", tt.in, err)
			continue
		}
		if got.String() != tt.want {
			t.Errorf("ParseRef(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestRefFormatting(t *testing.T) {
	r := NewRef(KindAPI, "", "Payments-API")
	if r.String() != "api:default/payments-api" {
		t.Fatalf("String = %s", r)
	}
	if r.Short() != "api:payments-api" {
		t.Fatalf("Short = %s", r.Short())
	}
	other := NewRef(KindGroup, "ops", "sre")
	if other.Short() != "group:ops/sre" {
		t.Fatalf("Short with namespace = %s", other.Short())
	}
	b, err := r.MarshalText()
	if err != nil || string(b) != "api:default/payments-api" {
		t.Fatalf("MarshalText = %s, %v", b, err)
	}
	if !(Ref{}).IsZero() || r.IsZero() {
		t.Fatal("IsZero wrong")
	}
}

func TestMustParseRefPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	MustParseRef("not a ref")
}

func TestParseKind(t *testing.T) {
	for _, s := range []string{"component", "API", "system", "GROUP", "Resource"} {
		if _, ok := ParseKind(s); !ok {
			t.Errorf("ParseKind(%q) failed", s)
		}
	}
	if _, ok := ParseKind("Domain"); ok {
		t.Error("Domain should be unsupported")
	}
}

func FuzzParseRef(f *testing.F) {
	for _, s := range []string{"component:a", "group:ns/b", "x", "api:/", ":::", "a/b/c", "resource:db-1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := ParseRef(s, KindComponent, DefaultNamespace)
		if err != nil {
			return
		}
		// Round trip: the canonical form must parse back to the same ref.
		r2, err := ParseRef(r.String(), "", DefaultNamespace)
		if err != nil || r2 != r {
			t.Fatalf("round trip of %q: %v -> %v (%v)", s, r, r2, err)
		}
	})
}
