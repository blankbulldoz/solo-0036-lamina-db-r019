package engine

import (
	"lamina/internal/catalog"
	"lamina/internal/datum"
	"lamina/internal/index"
	"lamina/internal/journal"
	"sort"
)

func (d *DB) unique(table string, writes map[string]map[string]staged) error {
	rows := d.store.Scan(table, d.sequence)
	overlay(rows, writes, table)
	for _, i := range d.catalog.Indexes {
		if i.Relation == table && i.Unique {
			if err := checkUnique(rows, i); err != nil {
				return err
			}
		}
	}
	return nil
}
func checkUnique(rows map[string]datum.Row, i catalog.Index) error {
	values := map[string]string{}
	for key, row := range rows {
		v := row[i.Column]
		if v.Null() {
			continue
		}
		token := v.Key()
		if prior, exists := values[token]; exists && prior != key {
			return fault("conflict", "unique constraint %s violated", i.Name)
		}
		values[token] = key
	}
	return nil
}

// rowChecks evaluates the declared checks of one relation against the final
// staged rows of the keys a transaction touches. Intermediate staged rows
// are never evaluated: only the row that would be published decides whether
// the commit is legal. Untouched rows keep versions that were validated by
// their own commit or by the check declaration itself, so the cost stays
// bounded by the transaction write set.
func (d *DB) rowChecks(table string, writes map[string]map[string]staged) error {
	checks := d.catalog.RelationChecks(table)
	if len(checks) == 0 {
		return nil
	}
	keys := make([]string, 0, len(writes[table]))
	for key := range writes[table] {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, definition := range checks {
		for _, key := range keys {
			m := writes[table][key]
			if m.Mutation.Deleted {
				continue
			}
			if definition.Violates(m.Mutation.Row) {
				return fault("conflict", "check %s violated by final row of %s key %s", definition.Name, table, key)
			}
		}
	}
	return nil
}
func (d *DB) CreateRelation(r catalog.Relation) (catalog.Relation, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.usable(); err != nil {
		return r, err
	}
	if err := r.Validate(); err != nil {
		return r, invalid(err)
	}
	if _, exists := d.catalog.Relations[r.Name]; exists {
		return r, fault("conflict", "relation exists")
	}
	r.Created = d.sequence + 1
	if err := d.publish(journal.Record{Kind: "relation", Relation: &r}); err != nil {
		return r, err
	}
	return r, nil
}
func (d *DB) CreateIndex(i catalog.Index) (catalog.Index, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.usable(); err != nil {
		return i, err
	}
	if err := d.catalog.CheckIndex(i); err != nil {
		return i, invalid(err)
	}
	if i.Unique {
		if err := checkUnique(d.store.Scan(i.Relation, d.sequence), i); err != nil {
			return i, err
		}
	}
	i.Created = d.sequence + 1
	if err := d.publish(journal.Record{Kind: "index", Index: &i}); err != nil {
		return i, err
	}
	d.indexes = index.Build(d.catalog, d.store)
	return i, nil
}

// CreateCheck declares a durable row check. The declaration is validated
// against every row currently visible in the relation; if existing data
// falsifies it, the declaration is rejected and nothing is published.
// Older history is not revalidated and stays readable as it was.
func (d *DB) CreateCheck(ch catalog.Check) (catalog.Check, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.usable(); err != nil {
		return ch, err
	}
	if err := d.catalog.ValidateCheck(ch); err != nil {
		return ch, invalid(err)
	}
	rows := d.store.Scan(ch.Relation, d.sequence)
	for _, key := range datum.Keys(rows) {
		if ch.Violates(rows[key]) {
			return ch, fault("conflict", "check %s conflicts with existing row of %s key %s", ch.Name, ch.Relation, key)
		}
	}
	ch.Created = d.sequence + 1
	if err := d.publish(journal.Record{Kind: "check", Check: &ch}); err != nil {
		return ch, err
	}
	return ch, nil
}
