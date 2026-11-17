package transport

import (
	"lamina/internal/catalog"
	"net/http"
)

func (s *Server) catalog(w http.ResponseWriter, r *http.Request) {
	relations, indexes, checks, err := s.db.Relations()
	if err != nil {
		respondError(w, err)
		return
	}
	write(w, 200, map[string]any{"relations": relations, "indexes": indexes, "checks": checks})
}
func (s *Server) createRelation(w http.ResponseWriter, r *http.Request) {
	var in catalog.Relation
	if !decode(w, r, &in) {
		return
	}
	out, err := s.db.CreateRelation(in)
	if err != nil {
		respondError(w, err)
		return
	}
	write(w, 201, out)
}
func (s *Server) createIndex(w http.ResponseWriter, r *http.Request) {
	var in catalog.Index
	if !decode(w, r, &in) {
		return
	}
	out, err := s.db.CreateIndex(in)
	if err != nil {
		respondError(w, err)
		return
	}
	write(w, 201, out)
}
func (s *Server) createCheck(w http.ResponseWriter, r *http.Request) {
	var in catalog.Check
	if !decode(w, r, &in) {
		return
	}
	out, err := s.db.CreateCheck(in)
	if err != nil {
		respondError(w, err)
		return
	}
	write(w, 201, out)
}
