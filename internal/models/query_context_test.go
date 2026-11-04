package models

import "testing"

func TestOrderClause(t *testing.T) {
	tests := []struct {
		name, orderBy, order, want string
	}{
		{"none", "", "", "libraries.created_at desc"},
		{"column only", "title", "", "libraries.title asc"},
		{"column and order", "title", "desc", "libraries.title desc"},
		{"created_at", "created_at", "asc", "libraries.created_at asc"},
		{"order only", "", "asc", "libraries.created_at desc"},
		{"unknown column", "description", "asc", "libraries.created_at desc"},
		{"injected column", "title;drop table libraries--", "", "libraries.created_at desc"},
		{"injected order", "title", "asc;drop table libraries--", "libraries.created_at desc"},
		{"qualified column", "libraries.title", "asc", "libraries.created_at desc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := QueryContext{OrderBy: tt.orderBy, Order: tt.order}
			if got := q.OrderClause("libraries"); got != tt.want {
				t.Errorf("OrderClause(%q, %q) = %q, want %q", tt.orderBy, tt.order, got, tt.want)
			}
		})
	}
}
