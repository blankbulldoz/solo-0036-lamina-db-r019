package catalog

import (
	"fmt"
	"sort"
)

type Index struct {
	Name     string `json:"name"`
	Relation string `json:"relation"`
	Column   string `json:"column"`
	Unique   bool   `json:"unique"`
	Created  uint64 `json:"created"`
}
type Catalog struct {
	Relations map[string]Relation `json:"relations"`
	Indexes   map[string]Index    `json:"indexes"`
	Checks    map[string]Check    `json:"checks"`
}

func New() *Catalog {
	return &Catalog{Relations: map[string]Relation{}, Indexes: map[string]Index{}, Checks: map[string]Check{}}
}
func (c *Catalog) Relation(name string) (Relation, error) {
	r, ok := c.Relations[name]
	if !ok {
		return r, fmt.Errorf("unknown relation %s", name)
	}
	return r, nil
}
func (c *Catalog) CheckIndex(i Index) error {
	if !ValidName(i.Name) {
		return fmt.Errorf("invalid index name")
	}
	if _, exists := c.Indexes[i.Name]; exists {
		return fmt.Errorf("index already exists")
	}
	r, err := c.Relation(i.Relation)
	if err != nil {
		return err
	}
	if _, ok := r.Column(i.Column); !ok {
		return fmt.Errorf("unknown index column")
	}
	return nil
}

// ValidateCheck verifies a new check declaration against the catalog: the
// name is well formed and unused, the relation exists and stays within the
// per-relation bound, and every predicate matches the relation schema.
func (c *Catalog) ValidateCheck(ch Check) error {
	if !ValidName(ch.Name) {
		return fmt.Errorf("invalid check name")
	}
	if _, exists := c.Checks[ch.Name]; exists {
		return fmt.Errorf("check already exists")
	}
	r, err := c.Relation(ch.Relation)
	if err != nil {
		return err
	}
	count := 0
	for _, existing := range c.Checks {
		if existing.Relation == ch.Relation {
			count++
		}
	}
	if count >= MaxChecksPerRelation {
		return fmt.Errorf("relation %s reaches %d checks", ch.Relation, MaxChecksPerRelation)
	}
	return ch.Validate(r)
}
func (c *Catalog) List() []Relation {
	out := make([]Relation, 0, len(c.Relations))
	for _, r := range c.Relations {
		r.Columns = append([]Column(nil), r.Columns...)
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (c *Catalog) IndexList() []Index {
	out := make([]Index, 0, len(c.Indexes))
	for _, v := range c.Indexes {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CheckList returns all declared checks ordered by name so enforcement and
// validation report the first violation deterministically.
func (c *Catalog) CheckList() []Check {
	out := make([]Check, 0, len(c.Checks))
	for _, v := range c.Checks {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RelationChecks returns the checks declared on one relation, ordered by name.
func (c *Catalog) RelationChecks(relation string) []Check {
	out := []Check{}
	for _, v := range c.Checks {
		if v.Relation == relation {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (c *Catalog) Clone() *Catalog {
	n := New()
	for _, r := range c.List() {
		n.Relations[r.Name] = r
	}
	for k, v := range c.Indexes {
		n.Indexes[k] = v
	}
	for k, v := range c.Checks {
		v.Predicates = append([]CheckPredicate(nil), v.Predicates...)
		n.Checks[k] = v
	}
	return n
}
