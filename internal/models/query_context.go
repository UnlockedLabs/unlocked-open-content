package models

import (
	"context"
	"math"
	"net/url"
	"slices"

	"github.com/UnlockedLabs/unlocked-contracts/features"
)

type PaginationMeta struct {
	CurrentPage int   `json:"current_page"`
	LastPage    int   `json:"last_page"`
	PerPage     int   `json:"per_page"`
	Total       int64 `json:"total"`
}

func NewPaginationInfo(currentPage, perPage int, total int64) PaginationMeta {
	lastPage := int(total) / perPage
	if int(total)%perPage > 0 {
		lastPage++
	}
	return PaginationMeta{
		CurrentPage: currentPage,
		LastPage:    lastPage,
		PerPage:     perPage,
		Total:       total,
	}
}

// QueryContext is a list request's paging, filters and caller.
type QueryContext struct {
	Params        url.Values
	Page          int
	PerPage       int
	FacilityID    uint
	UserID        uint
	OrderBy       string
	Order         string
	IsAdmin       bool
	Search        string
	Tags          []string
	Total         int64
	All           bool
	Ctx           context.Context
	FeatureAccess []features.Access
}

func (q *QueryContext) HasFeature(feature features.Access) bool {
	return slices.Contains(q.FeatureAccess, feature)
}

func (q QueryContext) IntoMeta() PaginationMeta {
	return NewPaginationInfo(q.Page, q.PerPage, q.Total)
}

func (q QueryContext) CalcOffset() int {
	return int(math.Abs(float64((q.Page - 1) * q.PerPage)))
}

// sortColumns are the columns a request can order a list by.
var sortColumns = []string{"title", "created_at"}

// OrderClause is the request's order on table, or newest first when the
// request has none or asks for a column or direction not allowed. OrderBy and
// Order come straight from the request, so only allowed values reach the SQL.
func (q QueryContext) OrderClause(table string) string {
	order := q.Order
	if order == "" {
		order = "asc"
	}
	if !slices.Contains(sortColumns, q.OrderBy) || (order != "asc" && order != "desc") {
		return table + ".created_at desc"
	}
	return table + "." + q.OrderBy + " " + order
}

func (q QueryContext) SearchQuery() string {
	if q.Search == "" {
		return ""
	}
	return "%" + q.Search + "%"
}
