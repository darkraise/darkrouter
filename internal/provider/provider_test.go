package provider

import "testing"

func TestResolvePicksHighestPriority(t *testing.T) {
	ps := []Provider{
		{ID: "low", Priority: 1, Models: []string{"m"}},
		{ID: "high", Priority: 10, Models: []string{"m"}},
	}
	got, ok := Resolve(ps, "m")
	if !ok || got.ID != "high" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestResolveFallsBackToDeclarationOrderOnEqualPriority(t *testing.T) {
	ps := []Provider{
		{ID: "first", Priority: 5, Models: []string{"m"}},
		{ID: "second", Priority: 5, Models: []string{"m"}},
	}
	got, _ := Resolve(ps, "m")
	if got.ID != "first" {
		t.Fatalf("got %q", got.ID)
	}
}

func TestResolveReportsMiss(t *testing.T) {
	if _, ok := Resolve([]Provider{{ID: "a", Models: []string{"x"}}}, "y"); ok {
		t.Fatal("expected a miss")
	}
}

func TestResolveSkipsProviderWithoutTheModel(t *testing.T) {
	ps := []Provider{
		{ID: "wrong", Priority: 99, Models: []string{"other"}},
		{ID: "right", Priority: 1, Models: []string{"m"}},
	}
	got, _ := Resolve(ps, "m")
	if got.ID != "right" {
		t.Fatalf("got %q", got.ID)
	}
}
