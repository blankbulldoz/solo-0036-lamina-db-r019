package catalog

import (
	"fmt"
	"lamina/internal/datum"
)

const (
	// MaxChecksPerRelation bounds the declarations evaluated inside the
	// commit lock for one relation.
	MaxChecksPerRelation = 8
	// MaxCheckPredicates bounds the conjunction of one check.
	MaxCheckPredicates = 8
)

// CheckPredicate is one bounded comparison of a column against a constant.
// It deliberately mirrors the query comparison operators; there are no
// expressions, scripts, disjunctions or column-to-column comparisons.
type CheckPredicate struct {
	Column string      `json:"column"`
	Op     string      `json:"op"`
	Value  datum.Value `json:"value"`
}

// Check is a durable, declarative row condition: a conjunction of bounded
// predicates evaluated under SQL three-valued logic. A row violates the
// check only when the conjunction evaluates to false; an unknown outcome
// (a null operand) is not a violation. Checks are journaled DDL and are
// stored in checkpoint and backup images as part of the catalog.
//
// Effective scope: a check governs rows committed at or after its Created
// sequence, and the declaration itself validates the rows visible at
// Created-1. Snapshots before Created-1 are served as they were and are
// not retroactively constrained.
type Check struct {
	Name       string           `json:"name"`
	Relation   string           `json:"relation"`
	Predicates []CheckPredicate `json:"predicates"`
	Created    uint64           `json:"created"`
}

// Validate verifies the check declaration against the relation schema.
func (ch Check) Validate(r Relation) error {
	if len(ch.Predicates) == 0 || len(ch.Predicates) > MaxCheckPredicates {
		return fmt.Errorf("check needs 1..%d predicates", MaxCheckPredicates)
	}
	for _, p := range ch.Predicates {
		c, ok := r.Column(p.Column)
		if !ok {
			return fmt.Errorf("unknown check column %s", p.Column)
		}
		switch p.Op {
		case "is_null", "not_null":
			if !p.Value.Null() {
				return fmt.Errorf("check operator %s takes no constant", p.Op)
			}
		case "=", "!=", "<", "<=", ">", ">=":
			if p.Value.Null() {
				return fmt.Errorf("check comparison %s needs a non-null constant; use is_null", p.Op)
			}
			if p.Value.Kind != c.Type {
				return fmt.Errorf("check constant type mismatch for %s", p.Column)
			}
		default:
			return fmt.Errorf("unsupported check operator %s", p.Op)
		}
	}
	return nil
}

// Three-valued truth of one predicate against a row.
const (
	truthFalse = iota
	truthUnknown
	truthTrue
)

func predicateTruth(p CheckPredicate, row datum.Row) int {
	v := row[p.Column]
	switch p.Op {
	case "is_null":
		if v.Null() {
			return truthTrue
		}
		return truthFalse
	case "not_null":
		if v.Null() {
			return truthFalse
		}
		return truthTrue
	}
	if v.Null() || p.Value.Null() {
		return truthUnknown
	}
	n, err := v.Compare(p.Value)
	if err != nil {
		// Declarations are validated against the schema and rows are
		// normalized, so a comparison error should be unreachable; a
		// constraint system fails closed if it ever happens.
		return truthFalse
	}
	pass := false
	switch p.Op {
	case "=":
		pass = n == 0
	case "!=":
		pass = n != 0
	case "<":
		pass = n < 0
	case "<=":
		pass = n <= 0
	case ">":
		pass = n > 0
	case ">=":
		pass = n >= 0
	}
	if pass {
		return truthTrue
	}
	return truthFalse
}

// Violates reports whether the row falsifies the conjunction. Unknown
// outcomes (null operands) do not violate the check; use not_null
// predicates to forbid nulls explicitly.
func (ch Check) Violates(row datum.Row) bool {
	for _, p := range ch.Predicates {
		if predicateTruth(p, row) == truthFalse {
			return true
		}
	}
	return false
}
